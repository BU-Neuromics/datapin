// Package invenio implements the backend.Backend interface against the
// InvenioRDM records API (plan §2.4). Zenodo runs InvenioRDM, so this one
// driver serves sandbox.zenodo.org, zenodo.org, and institutional
// InvenioRDM instances; Zenodo-specific quirks (hybrid legacy response
// shapes, D19) are handled defensively rather than per-profile.
package invenio

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/httpx"
)

// Client talks to one InvenioRDM instance.
type Client struct {
	base  string // e.g. https://sandbox.zenodo.org, no trailing slash
	token string
	http  *httpx.RetryClient
	caps  backend.Caps
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTP replaces the underlying transport (tests).
func WithHTTP(rc *httpx.RetryClient) Option {
	return func(c *Client) { c.http = rc }
}

// New returns a Client for the instance at baseURL. Pass an empty token for
// anonymous access (published-record reads only).
func New(baseURL, token string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid backend URL %q", baseURL)
	}
	c := &Client{
		base:  strings.TrimSuffix(baseURL, "/"),
		token: token,
		http:  httpx.New(&http.Client{Timeout: 60 * time.Second}),
		caps:  zenodoCaps(u.Host),
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// zenodoCaps is the Zenodo profile (plan §2.4); institutional instances
// share it until per-instance probing lands with the remote-add UX.
func zenodoCaps(host string) backend.Caps {
	return backend.Caps{
		MintsDOI:          true,
		ReserveDOI:        true,
		PIDKind:           "doi",
		SyncPublish:       true,
		ImportsPrevious:   true,
		MultipartUpload:   true,
		MaxFileSize:       50 << 30, // 50 GB/record default
		MaxFilesPerRecord: 100,
		ChecksumAlgo:      "md5",
		Sandbox:           strings.Contains(host, "sandbox"),
	}
}

// Capabilities implements backend.Backend.
func (c *Client) Capabilities() backend.Caps { return c.caps }

// Ping verifies the base URL answers like an InvenioRDM instance by
// fetching the (anonymous, instance-defined) resource-type vocabulary.
func (c *Client) Ping(ctx context.Context) error {
	var out struct {
		Hits struct {
			Total int `json:"total"`
		} `json:"hits"`
	}
	if err := c.doJSON(ctx, "GET", "/api/vocabularies/resourcetypes?size=1", nil, &out); err != nil {
		return fmt.Errorf("%s does not answer like an InvenioRDM instance: %w", c.base, err)
	}
	return nil
}

// --- wire types (hybrid legacy/RDM, read defensively per D19) ---

type recordJSON struct {
	ID           json.Number `json:"id"`
	ConceptRecID string      `json:"conceptrecid"`
	DOI          string      `json:"doi"`
	ConceptDOI   string      `json:"conceptdoi"`
	State        string      `json:"state"`
	Status       string      `json:"status"`
	Title        string      `json:"title"`
	PIDs         struct {
		DOI struct {
			Identifier string `json:"identifier"`
		} `json:"doi"`
	} `json:"pids"`
	Parent struct {
		ID string `json:"id"`
	} `json:"parent"`
	Metadata struct {
		Title     string `json:"title"`
		Relations struct {
			Version []struct {
				Index  int  `json:"index"`
				IsLast bool `json:"is_last"`
			} `json:"version"`
		} `json:"relations"`
	} `json:"metadata"`
	Created string         `json:"created"`
	Links   map[string]any `json:"links"`
}

// doi returns the version DOI from whichever shape carried it.
func (r *recordJSON) doi() string {
	if r.PIDs.DOI.Identifier != "" {
		return r.PIDs.DOI.Identifier
	}
	return r.DOI
}

// conceptID returns the parent/concept id from whichever shape carried it.
func (r *recordJSON) conceptID() string {
	if r.Parent.ID != "" {
		return r.Parent.ID
	}
	return r.ConceptRecID
}

func (r *recordJSON) title() string {
	if r.Metadata.Title != "" {
		return r.Metadata.Title
	}
	return r.Title
}

type fileJSON struct {
	Key      string      `json:"key"`
	Status   string      `json:"status"`
	Checksum string      `json:"checksum"`
	Size     json.Number `json:"size"`
	Links    struct {
		Content string `json:"content"`
		Commit  string `json:"commit"`
		Self    string `json:"self"`
	} `json:"links"`
}

func (f *fileJSON) toFileInfo() backend.FileInfo {
	fi := backend.FileInfo{Key: f.Key, Pending: f.Status != "completed"}
	if n, err := f.Size.Int64(); err == nil {
		fi.Size = n
	}
	if f.Checksum != "" {
		if sum, err := backend.ParseChecksum(f.Checksum); err == nil {
			fi.Checksum = sum
		}
	}
	return fi
}

type filesListJSON struct {
	Entries []fileJSON `json:"entries"`
}

type hitsJSON struct {
	Hits struct {
		Hits  []recordJSON `json:"hits"`
		Total int          `json:"total"`
	} `json:"hits"`
}

// errorJSON is the {status, message, errors[]} validation shape.
type errorJSON struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Errors  []struct {
		Field    string   `json:"field"`
		Messages []string `json:"messages"`
	} `json:"errors"`
}

// --- request plumbing ---

func (c *Client) newRequest(ctx context.Context, method, path string, body []byte, ctype string) (*http.Request, error) {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.base + path
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	return req, nil
}

// doJSON sends a JSON request and decodes the response into out (may be
// nil). Non-2xx responses map to typed errors: 404 → NotFoundError,
// validation 400s → ValidationError.
func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	return c.doJSONMarked(ctx, method, path, body, out, nil)
}

// doJSONMarked is doJSON with an optional request marker (e.g.
// httpx.OnlyRetry429 for non-idempotent actions).
func (c *Client) doJSONMarked(ctx context.Context, method, path string, body any, out any, mark func(*http.Request) *http.Request) error {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := c.newRequest(ctx, method, path, raw, "application/json")
	if err != nil {
		return err
	}
	if mark != nil {
		req = mark(req)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return c.apiError(resp, method, path)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// apiError converts a failed response to a typed error.
func (c *Client) apiError(resp *http.Response, method, path string) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return &backend.NotFoundError{What: method + " " + path}
	}
	var e errorJSON
	if err := json.Unmarshal(data, &e); err == nil && e.Message != "" {
		if len(e.Errors) > 0 {
			fields := map[string][]string{}
			for _, fe := range e.Errors {
				fields[fe.Field] = append(fields[fe.Field], fe.Messages...)
			}
			return &backend.ValidationError{Message: e.Message, Fields: fields}
		}
		return fmt.Errorf("%s %s: %s (HTTP %d)", method, path, e.Message, resp.StatusCode)
	}
	return fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
}

// --- metadata serialization ---

// rdmMetadata renders backend.Metadata as InvenioRDM draft metadata.
func rdmMetadata(m backend.Metadata) map[string]any {
	md := map[string]any{
		"title":            m.Title,
		"publication_date": m.PublicationDate,
		"resource_type":    map[string]any{"id": m.ResourceType},
	}
	if m.Publisher != "" {
		md["publisher"] = m.Publisher
	}
	if m.Description != "" {
		md["description"] = m.Description
	}
	if m.Version != "" {
		md["version"] = m.Version
	}
	if m.License != "" {
		// InvenioRDM vocabulary ids are lowercased SPDX.
		md["rights"] = []any{map[string]any{"id": strings.ToLower(m.License)}}
	}
	if len(m.Keywords) > 0 {
		subjects := make([]any, 0, len(m.Keywords))
		for _, k := range m.Keywords {
			subjects = append(subjects, map[string]any{"subject": k})
		}
		md["subjects"] = subjects
	}
	if len(m.Creators) > 0 {
		creators := make([]any, 0, len(m.Creators))
		for _, cr := range m.Creators {
			person := map[string]any{
				"type":        "personal",
				"family_name": cr.FamilyName,
				"given_name":  cr.GivenName,
			}
			if cr.ORCID != "" {
				person["identifiers"] = []any{map[string]any{"scheme": "orcid", "identifier": cr.ORCID}}
			}
			entry := map[string]any{"person_or_org": person}
			if cr.Affiliation != "" {
				entry["affiliations"] = []any{map[string]any{"name": cr.Affiliation}}
			}
			creators = append(creators, entry)
		}
		md["creators"] = creators
	}
	return md
}

// --- Backend implementation ---

// CreateDraft implements backend.Backend.
func (c *Client) CreateDraft(ctx context.Context, meta backend.Metadata) (backend.DraftID, error) {
	var rec recordJSON
	err := c.doJSON(ctx, "POST", "/api/records", map[string]any{
		"metadata": rdmMetadata(meta),
		"files":    map[string]any{"enabled": true},
	}, &rec)
	if err != nil {
		return "", fmt.Errorf("creating draft: %w", err)
	}
	return backend.DraftID(rec.ID.String()), nil
}

// UpdateMetadata implements backend.Backend.
func (c *Client) UpdateMetadata(ctx context.Context, id backend.DraftID, meta backend.Metadata) error {
	err := c.doJSON(ctx, "PUT", "/api/records/"+string(id)+"/draft", map[string]any{
		"metadata": rdmMetadata(meta),
		"files":    map[string]any{"enabled": true},
	}, nil)
	if err != nil {
		return fmt.Errorf("updating draft metadata: %w", err)
	}
	return nil
}

// UploadFile implements backend.Backend: register → stream content →
// commit → verify the server's checksum against the expected sum. The
// upload PUT streams r directly (no buffering); it is not retried.
func (c *Client) UploadFile(ctx context.Context, id backend.DraftID, key string, r io.Reader, size int64, sum backend.Checksum) (backend.FileInfo, error) {
	var reg filesListJSON
	err := c.doJSON(ctx, "POST", "/api/records/"+string(id)+"/draft/files",
		[]map[string]any{{"key": key}}, &reg)
	if err != nil {
		return backend.FileInfo{}, fmt.Errorf("registering %s: %w", key, err)
	}
	var entry *fileJSON
	for i := range reg.Entries {
		if reg.Entries[i].Key == key {
			entry = &reg.Entries[i]
		}
	}
	if entry == nil || entry.Links.Content == "" || entry.Links.Commit == "" {
		return backend.FileInfo{}, fmt.Errorf("registering %s: response carried no upload links", key)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", entry.Links.Content, r)
	if err != nil {
		return backend.FileInfo{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = size
	resp, err := c.http.Do(req)
	if err != nil {
		return backend.FileInfo{}, fmt.Errorf("uploading %s: %w", key, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return backend.FileInfo{}, fmt.Errorf("uploading %s: HTTP %d", key, resp.StatusCode)
	}

	var committed fileJSON
	if err := c.doJSON(ctx, "POST", entry.Links.Commit, nil, &committed); err != nil {
		return backend.FileInfo{}, fmt.Errorf("committing %s: %w", key, err)
	}
	fi := committed.toFileInfo()
	if fi.Checksum.Hex != "" && sum.Hex != "" && fi.Checksum != sum {
		return fi, fmt.Errorf("%s: server checksum %s does not match local checksum %s — upload corrupted", key, fi.Checksum, sum)
	}
	return fi, nil
}

// DeleteDraftFile implements backend.Backend.
func (c *Client) DeleteDraftFile(ctx context.Context, id backend.DraftID, key string) error {
	return c.doJSON(ctx, "DELETE", "/api/records/"+string(id)+"/draft/files/"+key, nil, nil)
}

// ListDraftFiles implements backend.Backend. Pending entries (registered
// but never committed — a crashed upload) block publish and must be
// cleared in preflight.
func (c *Client) ListDraftFiles(ctx context.Context, id backend.DraftID) ([]backend.FileInfo, error) {
	var list filesListJSON
	if err := c.doJSON(ctx, "GET", "/api/records/"+string(id)+"/draft/files", nil, &list); err != nil {
		return nil, err
	}
	return toFileInfos(list), nil
}

// ImportPreviousFiles implements backend.Backend: all-or-nothing copy of
// the previous version's files; only valid on an empty draft.
func (c *Client) ImportPreviousFiles(ctx context.Context, id backend.DraftID) error {
	return c.doJSON(ctx, "POST", "/api/records/"+string(id)+"/draft/actions/files-import", nil, nil)
}

// ReserveDOI implements backend.Backend.
func (c *Client) ReserveDOI(ctx context.Context, id backend.DraftID) (string, error) {
	var rec recordJSON
	if err := c.doJSON(ctx, "POST", "/api/records/"+string(id)+"/draft/pids/doi", nil, &rec); err != nil {
		return "", fmt.Errorf("reserving DOI: %w", err)
	}
	if rec.doi() == "" {
		return "", fmt.Errorf("reserving DOI: response carried no DOI")
	}
	return rec.doi(), nil
}

// Publish implements backend.Backend. On a 5xx (or an inconclusive error)
// it reconciles by re-GET: publish can fail at the gateway while
// succeeding server-side (zenodo#2131, D18), and a retry would see 404
// because the draft is gone.
func (c *Client) Publish(ctx context.Context, id backend.DraftID) (backend.PublishResult, error) {
	var rec recordJSON
	// OnlyRetry429: a publish 5xx may have succeeded server-side; the
	// recovery is the reconcile below, never a blind retry.
	err := c.doJSONMarked(ctx, "POST", "/api/records/"+string(id)+"/draft/actions/publish", nil, &rec, httpx.OnlyRetry429)
	if err == nil {
		return publishResult(rec), nil
	}
	var verr *backend.ValidationError
	if errors.As(err, &verr) {
		return backend.PublishResult{}, err // a 400 is conclusive
	}
	// Inconclusive (5xx, timeout, or 404 from a racing retry): reconcile.
	got, gerr := c.GetRecord(ctx, backend.RecordID(id))
	if gerr == nil && got.Published {
		return backend.PublishResult{
			RecordID: got.ID, DOI: got.DOI, ConceptDOI: got.ConceptDOI,
		}, nil
	}
	return backend.PublishResult{}, fmt.Errorf("publishing draft %s: %w", id, err)
}

func publishResult(rec recordJSON) backend.PublishResult {
	return backend.PublishResult{
		RecordID:   backend.RecordID(rec.ID.String()),
		DOI:        rec.doi(),
		ConceptDOI: rec.ConceptDOI,
	}
}

// Discard implements backend.Backend.
func (c *Client) Discard(ctx context.Context, id backend.DraftID) error {
	return c.doJSON(ctx, "DELETE", "/api/records/"+string(id)+"/draft", nil, nil)
}

// NewVersion implements backend.Backend. Idempotent server-side: while a
// version draft exists the same draft id is returned.
func (c *Client) NewVersion(ctx context.Context, rec backend.RecordID) (backend.DraftID, error) {
	var draft recordJSON
	if err := c.doJSON(ctx, "POST", "/api/records/"+string(rec)+"/versions", nil, &draft); err != nil {
		return "", fmt.Errorf("opening new version of %s: %w", rec, err)
	}
	return backend.DraftID(draft.ID.String()), nil
}

// GetRecord implements backend.Backend, including the version chain.
func (c *Client) GetRecord(ctx context.Context, id backend.RecordID) (backend.Record, error) {
	var rec recordJSON
	if err := c.doJSON(ctx, "GET", "/api/records/"+string(id), nil, &rec); err != nil {
		return backend.Record{}, err
	}
	out := backend.Record{
		ID:         backend.RecordID(rec.ID.String()),
		ConceptID:  rec.conceptID(),
		DOI:        rec.doi(),
		ConceptDOI: rec.ConceptDOI,
		Title:      rec.title(),
		Published:  true, // GET /api/records/{id} resolves published records only
	}
	var versions hitsJSON
	if err := c.doJSON(ctx, "GET", "/api/records/"+string(id)+"/versions", nil, &versions); err != nil {
		return out, fmt.Errorf("listing versions of %s: %w", id, err)
	}
	chain := versions.Hits.Hits
	// The API returns newest first; expose oldest first (chain order).
	for i := len(chain) - 1; i >= 0; i-- {
		v := chain[i]
		idx := 0
		isLast := false
		if len(v.Metadata.Relations.Version) > 0 {
			idx = v.Metadata.Relations.Version[0].Index
			isLast = v.Metadata.Relations.Version[0].IsLast
		}
		out.Versions = append(out.Versions, backend.VersionInfo{
			ID:       backend.RecordID(v.ID.String()),
			Index:    idx,
			DOI:      v.doi(),
			IsLatest: isLast,
			Created:  v.Created,
		})
	}
	return out, nil
}

// ListFiles implements backend.Backend for published records.
func (c *Client) ListFiles(ctx context.Context, rec backend.RecordID) ([]backend.FileInfo, error) {
	var list filesListJSON
	if err := c.doJSON(ctx, "GET", "/api/records/"+string(rec)+"/files", nil, &list); err != nil {
		return nil, err
	}
	return toFileInfos(list), nil
}

// DownloadFile implements backend.Backend, verifying the stream against
// the server's checksum (oc-checksum header when present, else the hash of
// what was received is the caller's to compare via the manifest pin).
func (c *Client) DownloadFile(ctx context.Context, rec backend.RecordID, key string, w io.Writer) error {
	req, err := c.newRequest(ctx, "GET", "/api/records/"+string(rec)+"/files/"+key+"/content", nil, "")
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return &backend.NotFoundError{What: "file " + key + " on record " + string(rec)}
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("downloading %s: HTTP %d", key, resp.StatusCode)
	}

	h := md5.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return fmt.Errorf("downloading %s: %w", key, err)
	}
	// Downloads carry oc-checksum: MD5:<hex>, not Content-MD5 (spike).
	if oc := resp.Header.Get("oc-checksum"); oc != "" {
		want, err := backend.ParseChecksum(oc)
		if err == nil && want.Algo == "md5" {
			got := hex.EncodeToString(h.Sum(nil))
			if got != want.Hex {
				return fmt.Errorf("downloading %s: stream MD5 %s does not match server checksum %s", key, got, want.Hex)
			}
		}
	}
	return nil
}

func toFileInfos(list filesListJSON) []backend.FileInfo {
	out := make([]backend.FileInfo, 0, len(list.Entries))
	for i := range list.Entries {
		out = append(out, list.Entries[i].toFileInfo())
	}
	return out
}
