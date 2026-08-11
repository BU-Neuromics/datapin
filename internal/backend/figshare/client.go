// Package figshare implements backend.Backend against the Figshare v2 API
// (docs.figshare.com). It is the adapter that proves Caps is real
// (plan §8 Phase 4): parted uploads, reserve-DOI, .vN version DOIs, and
// no files-import — a published article's draft keeps its files, so
// carrying unchanged content costs nothing without an import action.
//
// ⚠ Developed against fakefigshare (documented behavior); not yet
// verified against live Figshare — see docs/decisions.md D28.
package figshare

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
	"strconv"
	"strings"
	"time"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/httpx"
)

// Client talks to one Figshare instance.
type Client struct {
	base  string
	token string
	http  *httpx.RetryClient
	caps  backend.Caps
}

// New returns a Client for the API at baseURL (e.g.
// https://api.figshare.com); the /v2 prefix is appended if absent.
func New(baseURL, token string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid backend URL %q", baseURL)
	}
	base := strings.TrimSuffix(baseURL, "/")
	if !strings.HasSuffix(base, "/v2") {
		base += "/v2"
	}
	return &Client{
		base:  base,
		token: token,
		http:  httpx.New(&http.Client{Timeout: 60 * time.Second}),
		caps: backend.Caps{
			MintsDOI:          true,
			PerVersionDOI:     true,
			ReserveDOI:        true,
			PIDKind:           "doi",
			SyncPublish:       true,
			ImportsPrevious:   false, // the account draft retains files across versions
			MultipartUpload:   true,
			MaxFileSize:       20 << 30, // 20 GB free tier
			MaxFilesPerRecord: 500,
			ChecksumAlgo:      "md5",
			Sandbox:           strings.Contains(u.Host, "figsh.com"),
		},
	}, nil
}

// Capabilities implements backend.Backend.
func (c *Client) Capabilities() backend.Caps { return c.caps }

// Ping verifies the base URL answers like the Figshare API.
func (c *Client) Ping(ctx context.Context) error {
	var out []any
	if err := c.doJSON(ctx, "GET", "/account/licenses", nil, &out); err != nil {
		return fmt.Errorf("%s does not answer like the Figshare API: %w", c.base, err)
	}
	return nil
}

// --- record id scheme ---

// A figshare RecordID is "{articleID}" (latest) or "{articleID}.v{N}"
// (a specific published version). The article id doubles as the DraftID:
// Figshare drafts are the account article itself.
func parseRecordID(rec backend.RecordID) (articleID string, version int) {
	s := string(rec)
	if i := strings.LastIndex(s, ".v"); i > 0 {
		if v, err := strconv.Atoi(s[i+2:]); err == nil {
			return s[:i], v
		}
	}
	return s, 0
}

// --- wire types ---

type articleJSON struct {
	ID      json.Number `json:"id"`
	Title   string      `json:"title"`
	DOI     string      `json:"doi"`
	Version int         `json:"version"`
	Files   []fileJSON  `json:"files"`
}

type fileJSON struct {
	ID          json.Number `json:"id"`
	Name        string      `json:"name"`
	Size        int64       `json:"size"`
	SuppliedMD5 string      `json:"supplied_md5"`
	ComputedMD5 string      `json:"computed_md5"`
	Status      string      `json:"status"`
	UploadURL   string      `json:"upload_url"`
	DownloadURL string      `json:"download_url"`
}

func (f *fileJSON) toFileInfo() backend.FileInfo {
	sum := f.ComputedMD5
	if sum == "" {
		sum = f.SuppliedMD5
	}
	fi := backend.FileInfo{Key: f.Name, Size: f.Size, Pending: f.Status != "available" && f.Status != ""}
	if sum != "" {
		fi.Checksum = backend.Checksum{Algo: "md5", Hex: strings.ToLower(sum)}
	}
	return fi
}

type versionEntryJSON struct {
	Version int    `json:"version"`
	URL     string `json:"url"`
}

// --- plumbing ---

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.base + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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

func (c *Client) apiError(resp *http.Response, method, path string) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return &backend.NotFoundError{What: method + " " + path}
	}
	var e struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &e); err == nil && e.Message != "" {
		if resp.StatusCode == http.StatusBadRequest {
			return &backend.ValidationError{Message: e.Message}
		}
		return fmt.Errorf("%s %s: %s (HTTP %d)", method, path, e.Message, resp.StatusCode)
	}
	return fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
}

// --- metadata ---

// figshareMetadata renders backend.Metadata as article fields. The
// license is resolved against the instance's license vocabulary; an
// unmappable SPDX id is a loud, typed error — never silently omitted
// (which would publish under Figshare's own default license, D37).
func (c *Client) figshareMetadata(ctx context.Context, m backend.Metadata) (map[string]any, error) {
	md := map[string]any{
		"title":        m.Title,
		"defined_type": "dataset",
	}
	if m.Description != "" {
		md["description"] = m.Description
	}
	if len(m.Keywords) > 0 {
		md["tags"] = m.Keywords
	}
	if len(m.Creators) > 0 {
		var authors []any
		for _, cr := range m.Creators {
			name := strings.TrimSpace(cr.GivenName + " " + cr.FamilyName)
			author := map[string]any{"name": name}
			if cr.ORCID != "" {
				author["orcid_id"] = cr.ORCID
			}
			authors = append(authors, author)
		}
		md["authors"] = authors
	}
	if m.License != "" {
		id, err := c.licenseID(ctx, m.License)
		if err != nil {
			return nil, err
		}
		md["license"] = id
	}
	return md, nil
}

// licenseID maps an SPDX id onto the instance's license vocabulary by
// URL fragment (license ids are instance-defined integers). An id the
// instance cannot satisfy is a ValidationError listing what it offers.
func (c *Client) licenseID(ctx context.Context, spdx string) (int, error) {
	fragment := map[string]string{
		"cc0-1.0":      "publicdomain/zero",
		"cc-by-4.0":    "licenses/by/4.0",
		"cc-by-sa-4.0": "licenses/by-sa/4.0",
		"cc-by-nc-4.0": "licenses/by-nc/4.0",
		"mit":          "MIT",
		"apache-2.0":   "Apache",
		"gpl-3.0":      "gpl-3",
	}[strings.ToLower(spdx)]
	var licenses []struct {
		Value int    `json:"value"`
		Name  string `json:"name"`
		URL   string `json:"url"`
	}
	if err := c.doJSON(ctx, "GET", "/account/licenses", nil, &licenses); err != nil {
		return 0, fmt.Errorf("fetching the instance's license vocabulary: %w", err)
	}
	if fragment != "" {
		for _, l := range licenses {
			if strings.Contains(strings.ToLower(l.URL), strings.ToLower(fragment)) ||
				strings.Contains(strings.ToLower(l.Name), strings.ToLower(fragment)) {
				return l.Value, nil
			}
		}
	}
	var offered []string
	for _, l := range licenses {
		offered = append(offered, l.Name)
	}
	return 0, &backend.ValidationError{
		Message: fmt.Sprintf("this Figshare instance does not offer license %q; it offers: %s",
			spdx, strings.Join(offered, ", ")),
	}
}

// --- Backend implementation ---

// CreateDraft implements backend.Backend.
func (c *Client) CreateDraft(ctx context.Context, meta backend.Metadata) (backend.DraftID, error) {
	md, err := c.figshareMetadata(ctx, meta)
	if err != nil {
		return "", err
	}
	var out struct {
		EntityID json.Number `json:"entity_id"`
		Location string      `json:"location"`
	}
	if err := c.doJSON(ctx, "POST", "/account/articles", md, &out); err != nil {
		return "", fmt.Errorf("creating draft article: %w", err)
	}
	id := out.EntityID.String()
	if id == "" || id == "0" {
		// Fall back to the location tail.
		id = out.Location[strings.LastIndex(out.Location, "/")+1:]
	}
	return backend.DraftID(id), nil
}

// UpdateMetadata implements backend.Backend.
func (c *Client) UpdateMetadata(ctx context.Context, id backend.DraftID, meta backend.Metadata) error {
	md, err := c.figshareMetadata(ctx, meta)
	if err != nil {
		return err
	}
	return c.doJSON(ctx, "PUT", "/account/articles/"+string(id), md, nil)
}

// UploadFile implements backend.Backend via the parted flow: initiate
// (name/size/md5) → fetch upload parts → PUT each part → complete (the
// server verifies the supplied MD5). An existing same-name draft entry is
// replaced, keeping uploads idempotent across crashed runs.
func (c *Client) UploadFile(ctx context.Context, id backend.DraftID, key string, r io.Reader, size int64, sum backend.Checksum) (backend.FileInfo, error) {
	if existing, err := c.findDraftFile(ctx, id, key); err == nil && existing != nil {
		if err := c.doJSON(ctx, "DELETE", "/account/articles/"+string(id)+"/files/"+existing.ID.String(), nil, nil); err != nil {
			return backend.FileInfo{}, fmt.Errorf("replacing %s: %w", key, err)
		}
	}

	var initiated struct {
		Location string `json:"location"`
	}
	err := c.doJSON(ctx, "POST", "/account/articles/"+string(id)+"/files",
		map[string]any{"name": key, "size": size, "md5": sum.Hex}, &initiated)
	if err != nil {
		return backend.FileInfo{}, fmt.Errorf("initiating upload of %s: %w", key, err)
	}

	var info fileJSON
	if err := c.doJSON(ctx, "GET", initiated.Location, nil, &info); err != nil {
		return backend.FileInfo{}, fmt.Errorf("fetching upload info for %s: %w", key, err)
	}
	if info.UploadURL == "" {
		return backend.FileInfo{}, fmt.Errorf("upload info for %s carried no upload_url", key)
	}

	var parts struct {
		Parts []struct {
			PartNo      int   `json:"partNo"`
			StartOffset int64 `json:"startOffset"`
			EndOffset   int64 `json:"endOffset"`
		} `json:"parts"`
	}
	if err := c.doJSON(ctx, "GET", info.UploadURL, nil, &parts); err != nil {
		return backend.FileInfo{}, fmt.Errorf("fetching upload parts for %s: %w", key, err)
	}
	for _, p := range parts.Parts {
		length := p.EndOffset - p.StartOffset + 1
		if err := c.putPart(ctx, info.UploadURL, p.PartNo, io.LimitReader(r, length)); err != nil {
			return backend.FileInfo{}, fmt.Errorf("uploading part %d of %s: %w", p.PartNo, key, err)
		}
	}

	// Complete: POST the file endpoint; Figshare verifies the MD5.
	if err := c.doJSON(ctx, "POST", initiated.Location, nil, nil); err != nil {
		return backend.FileInfo{}, fmt.Errorf("completing upload of %s: %w", key, err)
	}
	var final fileJSON
	if err := c.doJSON(ctx, "GET", initiated.Location, nil, &final); err != nil {
		return backend.FileInfo{}, err
	}
	fi := final.toFileInfo()
	if fi.Checksum.Hex != "" && fi.Checksum != sum {
		return fi, fmt.Errorf("%s: server checksum %s does not match local %s — upload corrupted", key, fi.Checksum, sum)
	}
	return fi, nil
}

func (c *Client) putPart(ctx context.Context, uploadURL string, part int, r io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL+"/"+strconv.Itoa(part), r)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) findDraftFile(ctx context.Context, id backend.DraftID, key string) (*fileJSON, error) {
	var files []fileJSON
	if err := c.doJSON(ctx, "GET", "/account/articles/"+string(id)+"/files", nil, &files); err != nil {
		return nil, err
	}
	for i := range files {
		if files[i].Name == key {
			return &files[i], nil
		}
	}
	return nil, nil
}

// DeleteDraftFile implements backend.Backend.
func (c *Client) DeleteDraftFile(ctx context.Context, id backend.DraftID, key string) error {
	f, err := c.findDraftFile(ctx, id, key)
	if err != nil {
		return err
	}
	if f == nil {
		return &backend.NotFoundError{What: "draft file " + key}
	}
	return c.doJSON(ctx, "DELETE", "/account/articles/"+string(id)+"/files/"+f.ID.String(), nil, nil)
}

// ListDraftFiles implements backend.Backend.
func (c *Client) ListDraftFiles(ctx context.Context, id backend.DraftID) ([]backend.FileInfo, error) {
	var files []fileJSON
	if err := c.doJSON(ctx, "GET", "/account/articles/"+string(id)+"/files", nil, &files); err != nil {
		return nil, err
	}
	out := make([]backend.FileInfo, 0, len(files))
	for i := range files {
		out = append(out, files[i].toFileInfo())
	}
	return out, nil
}

// ImportPreviousFiles implements backend.Backend as a no-op: the account
// article's draft retains its files across published versions.
func (c *Client) ImportPreviousFiles(ctx context.Context, id backend.DraftID) error {
	return nil
}

// ReserveDOI implements backend.Backend.
func (c *Client) ReserveDOI(ctx context.Context, id backend.DraftID) (string, error) {
	var out struct {
		DOI string `json:"doi"`
	}
	if err := c.doJSON(ctx, "POST", "/account/articles/"+string(id)+"/reserve_doi", nil, &out); err != nil {
		return "", fmt.Errorf("reserving DOI: %w", err)
	}
	return out.DOI, nil
}

// Publish implements backend.Backend. Figshare's publish action is
// idempotent from our side (the article either gains a version or the
// request fails conclusively); the result is read back from the public
// article, so a gateway error reconciles the same way as a success.
func (c *Client) Publish(ctx context.Context, id backend.DraftID) (backend.PublishResult, error) {
	err := c.doJSON(ctx, "POST", "/account/articles/"+string(id)+"/publish", nil, nil)
	if err != nil {
		var verr *backend.ValidationError
		if errors.As(err, &verr) {
			return backend.PublishResult{}, err
		}
		// Inconclusive: reconcile by reading the public article below.
	}
	var pub articleJSON
	if gerr := c.doJSON(ctx, "GET", "/articles/"+string(id), nil, &pub); gerr != nil {
		if err != nil {
			return backend.PublishResult{}, fmt.Errorf("publishing article %s: %w", id, err)
		}
		return backend.PublishResult{}, fmt.Errorf("publish succeeded but reading the public article failed: %w", gerr)
	}
	return backend.PublishResult{
		RecordID:   backend.RecordID(fmt.Sprintf("%s.v%d", id, pub.Version)),
		DOI:        pub.DOI,
		ConceptDOI: baseDOI(pub.DOI),
	}, nil
}

// baseDOI strips the .vN suffix — Figshare's un-suffixed DOI resolves to
// the latest version, the concept-DOI role.
func baseDOI(doi string) string {
	if i := strings.LastIndex(doi, ".v"); i > 0 {
		if _, err := strconv.Atoi(doi[i+2:]); err == nil {
			return doi[:i]
		}
	}
	return doi
}

// Discard implements backend.Backend (unpublished drafts only — Figshare
// forbids deleting published articles, as immutability demands).
func (c *Client) Discard(ctx context.Context, id backend.DraftID) error {
	return c.doJSON(ctx, "DELETE", "/account/articles/"+string(id), nil, nil)
}

// NewVersion implements backend.Backend: the account article IS the next
// version's draft, so this resolves the article id. Trivially re-entrant.
func (c *Client) NewVersion(ctx context.Context, rec backend.RecordID) (backend.DraftID, error) {
	articleID, _ := parseRecordID(rec)
	// Verify it exists and is ours.
	var a articleJSON
	if err := c.doJSON(ctx, "GET", "/account/articles/"+articleID, nil, &a); err != nil {
		return "", fmt.Errorf("opening new version of %s: %w", rec, err)
	}
	return backend.DraftID(articleID), nil
}

// GetRecord implements backend.Backend.
func (c *Client) GetRecord(ctx context.Context, rec backend.RecordID) (backend.Record, error) {
	articleID, version := parseRecordID(rec)
	path := "/articles/" + articleID
	if version > 0 {
		path += "/versions/" + strconv.Itoa(version)
	}
	var a articleJSON
	if err := c.doJSON(ctx, "GET", path, nil, &a); err != nil {
		return backend.Record{}, err
	}
	out := backend.Record{
		ID:         rec,
		ConceptID:  articleID,
		DOI:        a.DOI,
		ConceptDOI: baseDOI(a.DOI),
		Title:      a.Title,
		Published:  true,
	}
	var versions []versionEntryJSON
	if err := c.doJSON(ctx, "GET", "/articles/"+articleID+"/versions", nil, &versions); err != nil {
		return out, fmt.Errorf("listing versions of %s: %w", articleID, err)
	}
	for _, v := range versions {
		out.Versions = append(out.Versions, backend.VersionInfo{
			ID:       backend.RecordID(fmt.Sprintf("%s.v%d", articleID, v.Version)),
			Index:    v.Version - 1,
			DOI:      fmt.Sprintf("%s.v%d", baseDOI(a.DOI), v.Version),
			IsLatest: v.Version == len(versions),
		})
	}
	return out, nil
}

// ListFiles implements backend.Backend for published records/versions.
func (c *Client) ListFiles(ctx context.Context, rec backend.RecordID) ([]backend.FileInfo, error) {
	files, err := c.publicFiles(ctx, rec)
	if err != nil {
		return nil, err
	}
	out := make([]backend.FileInfo, 0, len(files))
	for i := range files {
		out = append(out, files[i].toFileInfo())
	}
	return out, nil
}

func (c *Client) publicFiles(ctx context.Context, rec backend.RecordID) ([]fileJSON, error) {
	articleID, version := parseRecordID(rec)
	if version > 0 {
		var a articleJSON
		if err := c.doJSON(ctx, "GET", "/articles/"+articleID+"/versions/"+strconv.Itoa(version), nil, &a); err != nil {
			return nil, err
		}
		return a.Files, nil
	}
	var files []fileJSON
	if err := c.doJSON(ctx, "GET", "/articles/"+articleID+"/files", nil, &files); err != nil {
		return nil, err
	}
	return files, nil
}

// DownloadFile implements backend.Backend, verifying the stream against
// the listing's computed MD5.
func (c *Client) DownloadFile(ctx context.Context, rec backend.RecordID, key string, w io.Writer) error {
	files, err := c.publicFiles(ctx, rec)
	if err != nil {
		return err
	}
	var target *fileJSON
	for i := range files {
		if files[i].Name == key {
			target = &files[i]
		}
	}
	if target == nil {
		return &backend.NotFoundError{What: "file " + key + " on record " + string(rec)}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", target.DownloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("downloading %s: HTTP %d", key, resp.StatusCode)
	}
	h := md5.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return fmt.Errorf("downloading %s: %w", key, err)
	}
	if want := strings.ToLower(target.ComputedMD5); want != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			return fmt.Errorf("downloading %s: stream MD5 %s does not match listing checksum %s", key, got, want)
		}
	}
	return nil
}
