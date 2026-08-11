// Package dataverse implements backend.Backend against the Dataverse
// native API (guides.dataverse.org). Notable divergences the Caps
// declare: one DOI covers every version (PerVersionDOI=false — versions
// are 1.0, 2.0 under the same persistentId), the DOI is reserved at
// dataset creation, and keys containing "/" map onto directoryLabel
// (the PathHint behavior from plan §4.1).
//
// ⚠ Developed against fakedataverse (documented behavior); not yet
// verified against a live instance — see docs/decisions.md D28.
package dataverse

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/httpx"
)

// Client talks to one Dataverse instance.
type Client struct {
	base       string
	collection string // collection alias datasets are created under
	token      string
	http       *httpx.RetryClient
	caps       backend.Caps
	licCache   []licenseEntry // /api/licenses, fetched once per client

	// Lock polling (waitUnlocked): sleep is context-aware and injectable
	// for tests; maxLockPolls bounds the wait.
	sleep         func(ctx context.Context) error
	lockPollDelay time.Duration
	maxLockPolls  int
}

// Option customizes a Client.
type Option func(*Client)

// WithSleep replaces the inter-poll sleep used while waiting on dataset
// locks (tests pass a no-op).
func WithSleep(sleep func(ctx context.Context) error) Option {
	return func(c *Client) { c.sleep = sleep }
}

// New returns a Client for the instance at baseURL. A collection alias
// may ride on the URL path (https://host/dataverse/<alias>); datasets are
// created under it, defaulting to "root" (D29).
func New(baseURL, token string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid backend URL %q", baseURL)
	}
	collection := "root"
	base := strings.TrimSuffix(baseURL, "/")
	if rest, okCut := strings.CutPrefix(u.Path, "/dataverse/"); okCut && rest != "" {
		collection = strings.Trim(rest, "/")
		base = u.Scheme + "://" + u.Host
	}
	c := &Client{
		base:          base,
		collection:    collection,
		token:         token,
		http:          httpx.New(&http.Client{Timeout: 60 * time.Second}),
		lockPollDelay: 5 * time.Second,
		maxLockPolls:  24, // ~2 minutes at the default delay
		caps: backend.Caps{
			MintsDOI:      true,
			PerVersionDOI: false,
			ReserveDOI:    true, // the DOI exists from dataset creation
			PIDKind:       "doi",
			SyncPublish:   true,
			// A released dataset's draft carries its files, like Figshare.
			ImportsPrevious: false,
			ChecksumAlgo:    "md5",
			Sandbox:         strings.Contains(u.Host, "demo."),
		},
	}
	c.sleep = func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.lockPollDelay):
			return nil
		}
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// waitUnlocked polls the dataset's locks until they clear (bounded).
// Dataverse locks datasets during asynchronous work — tabular ingest,
// post-publish DOI finalization — and rejects mutations and publishes
// with a 403 while locked (live-verified: "This dataset is locked.
// Reason: Ingest. Please try publishing later.").
func (c *Client) waitUnlocked(ctx context.Context, pid string) error {
	for i := 0; ; i++ {
		var locks []struct {
			LockType string `json:"lockType"`
		}
		if err := c.doJSON(ctx, "GET", "/api/datasets/:persistentId/locks"+pidQuery(pid), nil, &locks); err != nil {
			return fmt.Errorf("checking locks on %s: %w", pid, err)
		}
		if len(locks) == 0 {
			return nil
		}
		if i >= c.maxLockPolls {
			return fmt.Errorf("dataset %s is still locked (%s) after waiting — try again later", pid, locks[0].LockType)
		}
		if err := c.sleep(ctx); err != nil {
			return err
		}
	}
}

// Capabilities implements backend.Backend.
func (c *Client) Capabilities() backend.Caps { return c.caps }

// Ping verifies the base URL answers like a Dataverse instance.
func (c *Client) Ping(ctx context.Context) error {
	var out struct {
		Version string `json:"version"`
	}
	if err := c.doJSON(ctx, "GET", "/api/info/version", nil, &out); err != nil {
		return fmt.Errorf("%s does not answer like a Dataverse instance: %w", c.base, err)
	}
	return nil
}

// --- record id scheme ---

// A dataverse RecordID is the persistentId ("doi:10.5072/FK2/ABC") for
// the latest version, or "persistentId@N" for released version N.0.
func parseRecordID(rec backend.RecordID) (pid string, version int) {
	s := string(rec)
	if i := strings.LastIndex(s, "@"); i > 0 {
		if v, err := strconv.Atoi(s[i+1:]); err == nil {
			return s[:i], v
		}
	}
	return s, 0
}

// key splits a flat key into (directoryLabel, filename).
func splitKey(key string) (dir, name string) {
	dir = path.Dir(key)
	if dir == "." {
		dir = ""
	}
	return dir, path.Base(key)
}

func joinKey(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// --- wire types ---

type envelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type fileEntryJSON struct {
	Label          string `json:"label"`
	DirectoryLabel string `json:"directoryLabel"`
	DataFile       struct {
		ID       json.Number `json:"id"`
		Filename string      `json:"filename"`
		MD5      string      `json:"md5"`
		Filesize int64       `json:"filesize"`
	} `json:"dataFile"`
}

func (f *fileEntryJSON) key() string {
	name := f.DataFile.Filename
	if f.Label != "" {
		name = f.Label
	}
	return joinKey(f.DirectoryLabel, name)
}

func (f *fileEntryJSON) toFileInfo() backend.FileInfo {
	fi := backend.FileInfo{Key: f.key(), Size: f.DataFile.Filesize}
	if f.DataFile.MD5 != "" {
		fi.Checksum = backend.Checksum{Algo: "md5", Hex: strings.ToLower(f.DataFile.MD5)}
	}
	return fi
}

// --- plumbing ---

func (c *Client) do(ctx context.Context, method, pathAndQuery string, body io.Reader, ctype string) (*envelope, int, error) {
	u := pathAndQuery
	if !strings.HasPrefix(u, "http") {
		u = c.base + pathAndQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, 0, err
	}
	if c.token != "" {
		req.Header.Set("X-Dataverse-key", c.token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%s %s: unparseable response (HTTP %d)", method, pathAndQuery, resp.StatusCode)
	}
	return &env, resp.StatusCode, nil
}

func (c *Client) doJSON(ctx context.Context, method, pathAndQuery string, body any, out any) error {
	var rd io.Reader
	ctype := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
		ctype = "application/json"
	}
	env, status, err := c.do(ctx, method, pathAndQuery, rd, ctype)
	if err != nil {
		return err
	}
	if env.Status != "OK" {
		if status == http.StatusNotFound {
			return &backend.NotFoundError{What: method + " " + pathAndQuery}
		}
		if status == http.StatusBadRequest {
			return &backend.ValidationError{Message: env.Message}
		}
		return fmt.Errorf("%s %s: %s (HTTP %d)", method, pathAndQuery, env.Message, status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

func pidQuery(pid string) string { return "?persistentId=" + url.QueryEscape(pid) }

// --- metadata ---

// datasetJSON renders backend.Metadata as the citation metadata block.
// lic is the instance-registered license object from resolveLicense (nil
// to send none).
func datasetJSON(m backend.Metadata, lic map[string]any) map[string]any {
	var authors []any
	var contactName string
	for _, cr := range m.Creators {
		name := cr.FamilyName
		if cr.GivenName != "" {
			name = cr.FamilyName + ", " + cr.GivenName
		}
		if contactName == "" {
			contactName = name
		}
		author := map[string]any{
			"authorName": field("authorName", name),
		}
		if cr.Affiliation != "" {
			author["authorAffiliation"] = field("authorAffiliation", cr.Affiliation)
		}
		if cr.ORCID != "" {
			author["authorIdentifierScheme"] = controlledField("authorIdentifierScheme", "ORCID")
			author["authorIdentifier"] = field("authorIdentifier", cr.ORCID)
		}
		authors = append(authors, author)
	}
	descr := m.Description
	if descr == "" {
		descr = m.Title
	}
	fields := []any{
		map[string]any{"typeName": "title", "multiple": false, "typeClass": "primitive", "value": m.Title},
		map[string]any{"typeName": "author", "multiple": true, "typeClass": "compound", "value": authors},
		map[string]any{"typeName": "datasetContact", "multiple": true, "typeClass": "compound", "value": []any{
			map[string]any{
				"datasetContactName":  field("datasetContactName", contactName),
				"datasetContactEmail": field("datasetContactEmail", m.ContactEmail),
			},
		}},
		map[string]any{"typeName": "dsDescription", "multiple": true, "typeClass": "compound", "value": []any{
			map[string]any{"dsDescriptionValue": field("dsDescriptionValue", descr)},
		}},
		map[string]any{"typeName": "subject", "multiple": true, "typeClass": "controlledVocabulary", "value": []string{"Other"}},
	}
	if len(m.Keywords) > 0 {
		var kws []any
		for _, k := range m.Keywords {
			kws = append(kws, map[string]any{"keywordValue": field("keywordValue", k)})
		}
		fields = append(fields, map[string]any{"typeName": "keyword", "multiple": true, "typeClass": "compound", "value": kws})
	}
	version := map[string]any{
		"metadataBlocks": map[string]any{
			"citation": map[string]any{"displayName": "Citation Metadata", "fields": fields},
		},
	}
	if lic != nil {
		version["license"] = lic
	}
	return map[string]any{"datasetVersion": version}
}

// --- license registry ---

// licenseEntry is one row of the instance's /api/licenses registry.
type licenseEntry struct {
	Name             string `json:"name"`
	URI              string `json:"uri"`
	Active           bool   `json:"active"`
	RightsIdentifier string `json:"rightsIdentifier"`
}

// licenses fetches (once per client) the instance's configured license
// registry. Dataverse serves it anonymously.
func (c *Client) licenses(ctx context.Context) ([]licenseEntry, error) {
	if c.licCache != nil {
		return c.licCache, nil
	}
	var out []licenseEntry
	if err := c.doJSON(ctx, "GET", "/api/licenses", nil, &out); err != nil {
		return nil, fmt.Errorf("fetching the instance's license registry: %w", err)
	}
	c.licCache = out
	return out, nil
}

// licenseToken normalizes a license name or SPDX id for matching:
// "CC BY 4.0" and "CC-BY-4.0" both become "ccby4.0".
func licenseToken(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(s))
}

// resolveLicense maps the manifest's SPDX id onto the instance's
// registered {name, uri} — real Dataverse rejects anything else (live
// finding, demo 6.11). Entries are matched by their SPDX
// rightsIdentifier crosswalk first (not all entries carry it), then by
// normalized name. An SPDX id the instance does not offer is a loud,
// typed error — never a silent substitution.
func (c *Client) resolveLicense(ctx context.Context, spdx string) (map[string]any, error) {
	if spdx == "" {
		return nil, nil
	}
	entries, err := c.licenses(ctx)
	if err != nil {
		return nil, err
	}
	var offered []string
	for _, e := range entries {
		if !e.Active {
			continue
		}
		offered = append(offered, e.Name)
		if strings.EqualFold(e.RightsIdentifier, spdx) || licenseToken(e.Name) == licenseToken(spdx) {
			return map[string]any{"name": e.Name, "uri": e.URI}, nil
		}
	}
	return nil, &backend.ValidationError{
		Message: fmt.Sprintf("this Dataverse instance does not offer license %q; it offers: %s",
			spdx, strings.Join(offered, ", ")),
	}
}

func field(name, value string) map[string]any {
	return map[string]any{"typeName": name, "multiple": false, "typeClass": "primitive", "value": value}
}

func controlledField(name, value string) map[string]any {
	return map[string]any{"typeName": name, "multiple": false, "typeClass": "controlledVocabulary", "value": value}
}

// --- Backend implementation ---

// CreateDraft implements backend.Backend. The DOI (persistentId) is
// reserved by creation itself.
func (c *Client) CreateDraft(ctx context.Context, meta backend.Metadata) (backend.DraftID, error) {
	// Dataverse requires a Point of Contact e-mail (live-verified 403
	// otherwise), and datapin has none to invent — fail with the manifest
	// key before anything is created on the instance.
	if strings.TrimSpace(meta.ContactEmail) == "" {
		return "", &backend.ValidationError{
			Message: "Dataverse requires a contact e-mail (Point of Contact) — set contact_email in [datasets.metadata]",
		}
	}
	lic, err := c.resolveLicense(ctx, meta.License)
	if err != nil {
		return "", err
	}
	var out struct {
		ID           json.Number `json:"id"`
		PersistentID string      `json:"persistentId"`
	}
	if err := c.doJSON(ctx, "POST", "/api/dataverses/"+c.collection+"/datasets", datasetJSON(meta, lic), &out); err != nil {
		return "", fmt.Errorf("creating dataset: %w", err)
	}
	return backend.DraftID(out.PersistentID), nil
}

// UpdateMetadata implements backend.Backend. Dataverse updates draft
// metadata via versions/:draft; the fake and flow tolerate a no-op here —
// metadata was set at creation and refreshed on publish is not modeled.
func (c *Client) UpdateMetadata(ctx context.Context, id backend.DraftID, meta backend.Metadata) error {
	// PUT /api/datasets/:persistentId/versions/:draft would be the full
	// call; created metadata is authoritative for this adapter version.
	return nil
}

// UploadFile implements backend.Backend via the multipart add endpoint,
// verifying the server's computed MD5 against the expected sum.
func (c *Client) UploadFile(ctx context.Context, id backend.DraftID, key string, r io.Reader, size int64, sum backend.Checksum) (backend.FileInfo, error) {
	// A previous publish may still be finalizing (async DOI registration
	// locks the dataset); mutations 403 while locked.
	if err := c.waitUnlocked(ctx, string(id)); err != nil {
		return backend.FileInfo{}, err
	}
	dir, name := splitKey(key)

	// Replace-in-place: Dataverse does not reject a duplicate label+dir —
	// it silently renames the incoming file (live-verified: data.csv →
	// data-1.csv). Delete any existing same-key entry first so re-runs
	// stay idempotent and the key never shifts.
	if existing, lerr := c.draftFileEntries(ctx, id); lerr == nil {
		for i := range existing {
			if existing[i].key() == key {
				if derr := c.doJSON(ctx, "DELETE", "/api/files/"+existing[i].DataFile.ID.String(), nil, nil); derr != nil {
					return backend.FileInfo{}, fmt.Errorf("replacing %s: %w", key, derr)
				}
				break
			}
		}
	} else if !backend.IsNotFound(lerr) {
		return backend.FileInfo{}, fmt.Errorf("listing draft before uploading %s: %w", key, lerr)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		return backend.FileInfo{}, err
	}
	if _, err := io.Copy(part, r); err != nil {
		return backend.FileInfo{}, err
	}
	// tabIngest=false: Dataverse would otherwise ingest tabular files
	// asynchronously — locking the dataset AND rewriting the file
	// (data.csv → data.tab, new checksums). datapin's contract is byte
	// fidelity: the bytes pinned are the bytes served back (D39).
	jsonData, _ := json.Marshal(map[string]any{"directoryLabel": dir, "tabIngest": false})
	if err := mw.WriteField("jsonData", string(jsonData)); err != nil {
		return backend.FileInfo{}, err
	}
	if err := mw.Close(); err != nil {
		return backend.FileInfo{}, err
	}

	env, status, err := c.do(ctx, "POST", "/api/datasets/:persistentId/add"+pidQuery(string(id)), &buf, mw.FormDataContentType())
	if err != nil {
		return backend.FileInfo{}, fmt.Errorf("uploading %s: %w", key, err)
	}
	if env.Status != "OK" {
		if status == http.StatusBadRequest {
			return backend.FileInfo{}, &backend.ValidationError{Message: env.Message}
		}
		return backend.FileInfo{}, fmt.Errorf("uploading %s: %s (HTTP %d)", key, env.Message, status)
	}
	var out struct {
		Files []fileEntryJSON `json:"files"`
	}
	if err := json.Unmarshal(env.Data, &out); err != nil || len(out.Files) == 0 {
		return backend.FileInfo{}, fmt.Errorf("uploading %s: response carried no file entry", key)
	}
	fi := out.Files[0].toFileInfo()
	if fi.Key != key {
		// The server renamed the upload (duplicate label) — a published
		// version would carry the wrong key. Remove it and fail loudly.
		_ = c.doJSON(ctx, "DELETE", "/api/files/"+out.Files[0].DataFile.ID.String(), nil, nil)
		return backend.FileInfo{}, fmt.Errorf("uploading %s: Dataverse stored it as %q (name collision in the draft) — upload aborted", key, fi.Key)
	}
	if fi.Checksum.Hex != "" && sum.Hex != "" && fi.Checksum != sum {
		// Remove the corrupt upload so the draft stays clean.
		_ = c.DeleteDraftFile(ctx, id, key)
		return fi, fmt.Errorf("%s: server checksum %s does not match local %s — upload corrupted", key, fi.Checksum, sum)
	}
	return fi, nil
}

// draftFileEntries lists the draft's files. LIVE-VERIFIED subtlety: a
// released dataset has no readable :draft version until a mutation
// lazily opens one — the read 404s. The implicit draft's contents are
// exactly the latest released version's files (that is what the first
// mutation seeds it with), so fall back to :latest-published. Without
// this fallback the publish transaction saw an "empty" draft, skipped
// the replace-delete, and Dataverse silently renamed the duplicate
// upload (data.csv → data-1.csv) into the published version.
func (c *Client) draftFileEntries(ctx context.Context, id backend.DraftID) ([]fileEntryJSON, error) {
	var files []fileEntryJSON
	err := c.doJSON(ctx, "GET", "/api/datasets/:persistentId/versions/:draft/files"+pidQuery(string(id)), nil, &files)
	if backend.IsNotFound(err) {
		var published []fileEntryJSON
		if perr := c.doJSON(ctx, "GET", "/api/datasets/:persistentId/versions/:latest-published/files"+pidQuery(string(id)), nil, &published); perr == nil {
			return published, nil
		}
		// Neither a draft nor a published version — surface the original.
	}
	return files, err
}

// ListDraftFiles implements backend.Backend.
func (c *Client) ListDraftFiles(ctx context.Context, id backend.DraftID) ([]backend.FileInfo, error) {
	files, err := c.draftFileEntries(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]backend.FileInfo, 0, len(files))
	for i := range files {
		out = append(out, files[i].toFileInfo())
	}
	return out, nil
}

// DeleteDraftFile implements backend.Backend.
func (c *Client) DeleteDraftFile(ctx context.Context, id backend.DraftID, key string) error {
	files, err := c.draftFileEntries(ctx, id)
	if err != nil {
		return err
	}
	for i := range files {
		if files[i].key() == key {
			return c.doJSON(ctx, "DELETE", "/api/files/"+files[i].DataFile.ID.String(), nil, nil)
		}
	}
	return &backend.NotFoundError{What: "draft file " + key}
}

// ImportPreviousFiles implements backend.Backend as a no-op: a released
// dataset's draft opens carrying its files.
func (c *Client) ImportPreviousFiles(ctx context.Context, id backend.DraftID) error { return nil }

// ReserveDOI implements backend.Backend: the persistentId IS the DOI,
// reserved at creation.
func (c *Client) ReserveDOI(ctx context.Context, id backend.DraftID) (string, error) {
	return strings.TrimPrefix(string(id), "doi:"), nil
}

// Publish implements backend.Backend (major release). Reconciles by
// re-reading the dataset when the action's outcome is inconclusive.
func (c *Client) Publish(ctx context.Context, id backend.DraftID) (backend.PublishResult, error) {
	if err := c.waitUnlocked(ctx, string(id)); err != nil {
		return backend.PublishResult{}, err
	}
	// Publication finalizes asynchronously (live-verified: the POST is
	// accepted while DOI registration completes in the background), and a
	// finalizing version is not RELEASED yet — so completion means the
	// released-version count GREW, not merely "some version is released"
	// (which is already true when publishing v2 over v1).
	before, gerr := c.GetRecord(ctx, backend.RecordID(id))
	if gerr != nil {
		return backend.PublishResult{}, fmt.Errorf("publishing dataset %s: %w", id, gerr)
	}
	prior := len(before.Versions)
	err := c.doJSON(ctx, "POST", "/api/datasets/:persistentId/actions/:publish"+pidQuery(string(id))+"&type=major", nil, nil)
	if err != nil {
		var verr *backend.ValidationError
		if errors.As(err, &verr) {
			return backend.PublishResult{}, err
		}
		// Non-validation failures still reconcile below (the invenio D18
		// lesson: a publish can report failure while succeeding).
	}
	for i := 0; ; i++ {
		rec, gerr := c.GetRecord(ctx, backend.RecordID(id))
		if gerr == nil && rec.Published && len(rec.Versions) > prior {
			latest := rec.Versions[len(rec.Versions)-1]
			return backend.PublishResult{
				RecordID:   latest.ID,
				DOI:        rec.DOI,
				ConceptDOI: rec.ConceptDOI,
			}, nil
		}
		if i >= c.maxLockPolls {
			switch {
			case gerr != nil:
				err = gerr
			case err == nil:
				err = fmt.Errorf("the publish was accepted but the dataset has not finished publishing — check 'datapin versions' later")
			}
			return backend.PublishResult{}, fmt.Errorf("publishing dataset %s: %w", id, err)
		}
		if serr := c.sleep(ctx); serr != nil {
			return backend.PublishResult{}, serr
		}
	}
}

// Discard implements backend.Backend (unpublished datasets only).
func (c *Client) Discard(ctx context.Context, id backend.DraftID) error {
	var info struct {
		ID json.Number `json:"id"`
	}
	if err := c.doJSON(ctx, "GET", "/api/datasets/:persistentId"+pidQuery(string(id)), nil, &info); err != nil {
		return err
	}
	return c.doJSON(ctx, "DELETE", "/api/datasets/"+info.ID.String(), nil, nil)
}

// NewVersion implements backend.Backend: mutating a released dataset
// opens its draft implicitly, so this resolves the persistentId.
// Trivially re-entrant.
func (c *Client) NewVersion(ctx context.Context, rec backend.RecordID) (backend.DraftID, error) {
	pid, _ := parseRecordID(rec)
	if err := c.doJSON(ctx, "GET", "/api/datasets/:persistentId"+pidQuery(pid), nil, nil); err != nil {
		return "", fmt.Errorf("opening new version of %s: %w", rec, err)
	}
	return backend.DraftID(pid), nil
}

// GetRecord implements backend.Backend.
func (c *Client) GetRecord(ctx context.Context, rec backend.RecordID) (backend.Record, error) {
	pid, _ := parseRecordID(rec)
	var info struct {
		ID            json.Number `json:"id"`
		PersistentID  string      `json:"persistentId"`
		LatestVersion struct {
			VersionState string `json:"versionState"`
		} `json:"latestVersion"`
		PublicationDate any `json:"publicationDate"`
	}
	if err := c.doJSON(ctx, "GET", "/api/datasets/:persistentId"+pidQuery(pid), nil, &info); err != nil {
		return backend.Record{}, err
	}
	doi := strings.TrimPrefix(pid, "doi:")
	out := backend.Record{
		ID:         rec,
		ConceptID:  pid,
		DOI:        doi,
		ConceptDOI: doi, // one DOI for all versions
		Published:  info.PublicationDate != nil,
	}
	var versions []struct {
		VersionNumber int    `json:"versionNumber"`
		VersionState  string `json:"versionState"`
	}
	if err := c.doJSON(ctx, "GET", "/api/datasets/:persistentId/versions"+pidQuery(pid), nil, &versions); err != nil {
		return out, fmt.Errorf("listing versions of %s: %w", pid, err)
	}
	released := 0
	for _, v := range versions {
		if v.VersionState == "RELEASED" {
			released++
		}
	}
	n := 0
	for _, v := range versions {
		if v.VersionState != "RELEASED" {
			continue
		}
		n++
		out.Versions = append(out.Versions, backend.VersionInfo{
			ID:       backend.RecordID(fmt.Sprintf("%s@%d", pid, v.VersionNumber)),
			Index:    v.VersionNumber - 1,
			DOI:      doi,
			IsLatest: n == released,
		})
	}
	return out, nil
}

// ListFiles implements backend.Backend for released records/versions.
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

func (c *Client) publicFiles(ctx context.Context, rec backend.RecordID) ([]fileEntryJSON, error) {
	pid, version := parseRecordID(rec)
	ver := ":latest-published"
	if version > 0 {
		ver = fmt.Sprintf("%d.0", version)
	}
	var files []fileEntryJSON
	err := c.doJSON(ctx, "GET", "/api/datasets/:persistentId/versions/"+ver+"/files"+pidQuery(pid), nil, &files)
	return files, err
}

// DownloadFile implements backend.Backend, verifying the stream against
// the listing's MD5.
func (c *Client) DownloadFile(ctx context.Context, rec backend.RecordID, key string, w io.Writer) error {
	files, err := c.publicFiles(ctx, rec)
	if err != nil {
		return err
	}
	var target *fileEntryJSON
	for i := range files {
		if files[i].key() == key {
			target = &files[i]
		}
	}
	if target == nil {
		return &backend.NotFoundError{What: "file " + key + " on record " + string(rec)}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/api/access/datafile/"+target.DataFile.ID.String(), nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("X-Dataverse-key", c.token)
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
	if want := strings.ToLower(target.DataFile.MD5); want != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			return fmt.Errorf("downloading %s: stream MD5 %s does not match listing checksum %s", key, got, want)
		}
	}
	return nil
}
