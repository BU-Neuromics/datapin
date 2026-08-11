// Package fakeinvenio is a hermetic, in-process fake of the InvenioRDM API
// as Zenodo sandbox actually behaves (docs/zenodo-notes.md, D17): hybrid
// legacy/RDM response shapes, idempotent POST /versions, all-or-nothing
// files-import on an empty draft only, an atomic 100-file cap enforced at
// registration, empty files accepted by default, rate-limit headers (and
// retry-after) on every response, publish-twice → 404.
//
// The fixtures in fixtures/ are the source of truth for these shapes.
//
// ⚠ Multipart (`M` transfer) — partially DOCUMENTED-only (D28/D43): the
// registration request/response shape (per-part URLs with expirations under
// links.parts) is fixture-verified (fixture 39), but the sandbox spike never
// uploaded parts, so the part-PUT, commit, and abort behaviors here are
// modeled from InvenioRDM's documented multipart transfer provider
// (invenio-records-resources services/files/transfer/providers/multipart.py):
// registration requires parts, size, and part_size; direct content PUTs are
// rejected; non-final parts must match part_size exactly; commit with missing
// parts is rejected; after commit the transfer type flips to "L"; storage-
// specific minimums (e.g. S3's 5 MiB part floor) are NOT modeled. Treat the
// first live multipart run as a verification spike.
package fakeinvenio

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Server is the fake. Zero value is not usable; call New.
type Server struct {
	ts    *httptest.Server
	token string

	mu      sync.Mutex
	nextID  int
	records map[string]*record // by record id
	// concepts maps concept id -> record ids, oldest first.
	concepts map[string][]string

	// Knobs (set before use; D17 — defaults mirror observed sandbox).
	RejectEmptyFiles bool
	MaxFiles         int
	// FailPublish makes the next publish of the given draft id return the
	// given status once while still publishing server-side when 5xx — the
	// "publish can 504 while succeeding" divergence (zenodo#2131).
	FailPublish map[string]int
	// Throttle429 makes the next N requests answer 429 (Retry-After: 0),
	// for asserting the retry wiring.
	Throttle429 int
	// FailPart makes the PUT of the given part number of the given key
	// answer 500 once (key → part number), for asserting clean aborts.
	FailPart map[string]int
	// AsyncMultipartChecksum makes multipart commits return the
	// "multipart:{etag}-{part_size}" placeholder instead of an md5 —
	// modeling the documented asynchronous checksum recomputation.
	AsyncMultipartChecksum bool

	requests []string
}

type record struct {
	id          string
	conceptID   string
	index       int
	published   bool
	doi         string
	conceptDOI  string
	reservedDOI string
	meta        map[string]any
	files       map[string]*file
	fileOrder   []string
}

type file struct {
	key       string
	data      []byte
	committed bool
	md5hex    string
	checksum  string // wire form reported at/after commit ("md5:…" or the multipart placeholder)
	transfer  string
	parts     int
	partSize  int64
	partData  [][]byte // 0-indexed part contents; nil = part not uploaded
	size      int64
}

// New starts the fake accepting the given bearer token.
func New(token string) *Server {
	s := &Server{
		token:    token,
		nextID:   100000,
		records:  map[string]*record{},
		concepts: map[string][]string{},
		MaxFiles: 100,
	}
	s.ts = httptest.NewServer(http.HandlerFunc(s.route))
	return s
}

// URL returns the base URL (stands in for https://sandbox.zenodo.org).
func (s *Server) URL() string { return s.ts.URL }

// Close shuts the server down.
func (s *Server) Close() { s.ts.Close() }

// ListRequests returns "METHOD path" for every request served, for
// request-count assertions (the fakeosf pattern).
func (s *Server) ListRequests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// PublishedIDs returns the ids of all published records, sorted.
func (s *Server) PublishedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, r := range s.records {
		if r.published {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	throttled := s.Throttle429 > 0
	if throttled {
		s.Throttle429--
	}
	s.mu.Unlock()
	if throttled {
		w.Header().Set("Retry-After", "0")
		w.Header().Set("X-RateLimit-Remaining", "0")
		jsonError(w, 429, "30 per 1 minute")
		return
	}

	// Rate-limit headers ride on every response, and retry-after appears
	// even on 200s (zenodo-notes §3) — clients must not misread it.
	w.Header().Set("X-RateLimit-Limit", "133")
	w.Header().Set("X-RateLimit-Remaining", "100")
	w.Header().Set("X-RateLimit-Reset", "1786405055")
	w.Header().Set("Retry-After", "9")

	if !s.authorized(r) {
		jsonError(w, 401, "The server could not verify your credentials.")
		return
	}

	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/records" && r.Method == "POST":
		s.createDraft(w, r)
	case path == "/api/user/records" && r.Method == "GET":
		s.userRecords(w, r)
	case path == "/api/vocabularies/resourcetypes" && r.Method == "GET":
		writeJSON(w, 200, map[string]any{"hits": map[string]any{
			"hits":  []any{map[string]any{"id": "dataset"}, map[string]any{"id": "software"}, map[string]any{"id": "publication"}},
			"total": 3,
		}})
	case strings.HasPrefix(path, "/api/records/"):
		s.recordRoute(w, r, strings.TrimPrefix(path, "/api/records/"))
	default:
		jsonError(w, 404, "Not found.")
	}
}

// recordRoute dispatches /api/records/{id}[/...] paths.
func (s *Server) recordRoute(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	id := parts[0]
	sub := parts[1:]

	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		jsonError(w, 404, "The persistent identifier does not exist.")
		return
	}

	switch {
	// --- published-record surface ---
	case len(sub) == 0 && r.Method == "GET":
		if !rec.published {
			jsonError(w, 404, "The persistent identifier does not exist.")
			return
		}
		writeJSON(w, 200, s.recordJSON(rec, false))
	case len(sub) == 1 && sub[0] == "versions" && r.Method == "GET":
		s.versionsList(w, rec)
	case len(sub) == 1 && sub[0] == "versions" && r.Method == "POST":
		s.newVersion(w, rec)
	case len(sub) == 1 && sub[0] == "files" && r.Method == "GET":
		if !rec.published {
			jsonError(w, 404, "Not found.")
			return
		}
		writeJSON(w, 200, s.filesJSON(rec))
	case len(sub) == 3 && sub[0] == "files" && sub[2] == "content" && r.Method == "GET":
		s.download(w, rec, sub[1])

	// --- draft surface ---
	case len(sub) == 1 && sub[0] == "draft":
		s.draftRoute(w, r, rec)
	case len(sub) >= 2 && sub[0] == "draft":
		s.draftSubRoute(w, r, rec, sub[1:])
	default:
		jsonError(w, 404, "Not found.")
	}
}

func (s *Server) draftRoute(w http.ResponseWriter, r *http.Request, rec *record) {
	if rec.published {
		// After publish the draft resource is gone (publish-twice → 404).
		jsonError(w, 404, "Not found.")
		return
	}
	switch r.Method {
	case "GET":
		writeJSON(w, 200, s.recordJSON(rec, true))
	case "PUT":
		var body struct {
			Metadata map[string]any `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, 400, "Malformed request.")
			return
		}
		rec.meta = body.Metadata
		writeJSON(w, 200, s.recordJSON(rec, true))
	case "DELETE":
		delete(s.records, rec.id)
		chain := s.concepts[rec.conceptID]
		for i, rid := range chain {
			if rid == rec.id {
				s.concepts[rec.conceptID] = append(chain[:i], chain[i+1:]...)
				break
			}
		}
		w.WriteHeader(204)
	default:
		jsonError(w, 405, "Method not allowed.")
	}
}

func (s *Server) draftSubRoute(w http.ResponseWriter, r *http.Request, rec *record, sub []string) {
	if rec.published {
		jsonError(w, 404, "Not found.")
		return
	}
	switch {
	case len(sub) == 1 && sub[0] == "files" && r.Method == "POST":
		s.registerFiles(w, r, rec)
	case len(sub) == 1 && sub[0] == "files" && r.Method == "GET":
		writeJSON(w, 200, s.filesJSON(rec))
	case len(sub) == 2 && sub[0] == "files":
		s.draftFile(w, r, rec, sub[1], "")
	case len(sub) == 3 && sub[0] == "files":
		s.draftFile(w, r, rec, sub[1], sub[2])
	case len(sub) == 4 && sub[0] == "files" && sub[2] == "content" && r.Method == "PUT":
		s.partUpload(w, r, rec, sub[1], sub[3])
	case len(sub) == 2 && sub[0] == "actions" && sub[1] == "publish" && r.Method == "POST":
		s.publish(w, rec)
	case len(sub) == 2 && sub[0] == "actions" && sub[1] == "files-import" && r.Method == "POST":
		s.filesImport(w, rec)
	case len(sub) == 2 && sub[0] == "pids" && sub[1] == "doi" && r.Method == "POST":
		rec.reservedDOI = "10.5072/zenodo." + rec.id
		writeJSON(w, 201, s.recordJSON(rec, true))
	default:
		jsonError(w, 404, "Not found.")
	}
}

func (s *Server) createDraft(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, 400, "Malformed request.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conceptID := s.allocID()
	id := s.allocID()
	rec := &record{id: id, conceptID: conceptID, meta: body.Metadata, files: map[string]*file{}}
	s.records[id] = rec
	s.concepts[conceptID] = []string{id}
	writeJSON(w, 201, s.recordJSON(rec, true))
}

func (s *Server) registerFiles(w http.ResponseWriter, r *http.Request, rec *record) {
	var entries []struct {
		Key      string `json:"key"`
		Size     int64  `json:"size"`
		Transfer struct {
			Type     string `json:"type"`
			Parts    int    `json:"parts"`
			PartSize int64  `json:"part_size"`
		} `json:"transfer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
		jsonError(w, 400, "Malformed request.")
		return
	}
	// The cap is enforced at registration and the batch is atomic
	// (zenodo-notes §7): over the limit → 400, zero entries registered.
	if len(rec.files)+len(entries) > s.MaxFiles {
		jsonError(w, 400, "Uploading selected files will result in exceeding the max amount per record.")
		return
	}
	for _, e := range entries {
		if _, dup := rec.files[e.Key]; dup {
			jsonError(w, 400, fmt.Sprintf("File with key %s already exists.", e.Key))
			return
		}
	}
	// Multipart registrations require parts, size, and part_size — the
	// provider raises TransferException for each (DOCUMENTED-only, D43).
	for _, e := range entries {
		if e.Transfer.Type != "M" {
			continue
		}
		switch {
		case e.Transfer.Parts <= 0:
			jsonError(w, 400, "Multipart file transfer requires parts.")
			return
		case e.Size <= 0:
			jsonError(w, 400, "Multipart file transfer requires file size.")
			return
		case e.Transfer.PartSize <= 0:
			jsonError(w, 400, "Multipart file transfer to local storage requires part_size.")
			return
		}
	}
	for _, e := range entries {
		tt := e.Transfer.Type
		if tt == "" {
			tt = "L"
		}
		f := &file{key: e.Key, transfer: tt, parts: e.Transfer.Parts, partSize: e.Transfer.PartSize, size: e.Size}
		if tt == "M" {
			f.partData = make([][]byte, f.parts)
		}
		rec.files[e.Key] = f
		rec.fileOrder = append(rec.fileOrder, e.Key)
	}
	writeJSON(w, 201, s.filesJSON(rec))
}

func (s *Server) draftFile(w http.ResponseWriter, r *http.Request, rec *record, key, action string) {
	f, ok := rec.files[key]
	if !ok {
		jsonError(w, 404, "Not found.")
		return
	}
	switch {
	case action == "content" && r.Method == "PUT":
		if f.transfer == "M" {
			// Documented provider behavior (D43): multipart content
			// arrives via the part URLs, never a direct PUT.
			jsonError(w, 400, "Can not set content for multipart file, use the parts instead.")
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			jsonError(w, 400, "Malformed request.")
			return
		}
		if s.RejectEmptyFiles && len(data) == 0 {
			jsonError(w, 400, "Empty files are not accepted.")
			return
		}
		f.data = data
		writeJSON(w, 200, s.fileJSON(rec, f))
	case action == "commit" && r.Method == "POST":
		if f.transfer == "M" {
			s.commitMultipart(w, rec, f)
			return
		}
		sum := md5.Sum(f.data)
		f.md5hex = hex.EncodeToString(sum[:])
		f.checksum = "md5:" + f.md5hex
		f.committed = true
		f.size = int64(len(f.data))
		writeJSON(w, 200, s.fileJSON(rec, f))
	case action == "" && r.Method == "DELETE":
		delete(rec.files, key)
		for i, k := range rec.fileOrder {
			if k == key {
				rec.fileOrder = append(rec.fileOrder[:i], rec.fileOrder[i+1:]...)
				break
			}
		}
		w.WriteHeader(204)
	case action == "" && r.Method == "GET":
		writeJSON(w, 200, s.fileJSON(rec, f))
	default:
		jsonError(w, 404, "Not found.")
	}
}

// partUpload stores one part of a multipart (`M`) file. Non-final parts
// must match the declared part_size exactly (the documented local-storage
// provider validation, D43).
func (s *Server) partUpload(w http.ResponseWriter, r *http.Request, rec *record, key, partStr string) {
	f, ok := rec.files[key]
	if !ok || f.transfer != "M" {
		jsonError(w, 404, "Not found.")
		return
	}
	part, err := strconv.Atoi(partStr)
	if err != nil || part < 1 || part > f.parts {
		jsonError(w, 404, "Not found.")
		return
	}
	if s.FailPart != nil {
		if n, hit := s.FailPart[key]; hit && n == part {
			delete(s.FailPart, key)
			jsonError(w, 500, "Injected part failure.")
			return
		}
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, 400, "Malformed request.")
		return
	}
	want := f.partSize
	if part == f.parts {
		want = f.size - int64(f.parts-1)*f.partSize
	}
	if int64(len(data)) != want {
		jsonError(w, 400, fmt.Sprintf("Part %d has unexpected size %d, expected %d.", part, len(data), want))
		return
	}
	f.partData[part-1] = data
	writeJSON(w, 200, s.fileJSON(rec, f))
}

// commitMultipart completes a multipart upload: every part must be present
// (DOCUMENTED-only — the storage backend cannot complete with parts
// missing), the bytes reassemble in part order, and the transfer type flips
// to "L" as the documented provider does.
func (s *Server) commitMultipart(w http.ResponseWriter, rec *record, f *file) {
	var data []byte
	for i, p := range f.partData {
		if p == nil {
			jsonError(w, 400, fmt.Sprintf("Part %d has not been uploaded yet.", i+1))
			return
		}
		data = append(data, p...)
	}
	if int64(len(data)) != f.size {
		jsonError(w, 400, "Uploaded parts do not add up to the declared file size.")
		return
	}
	f.data = data
	sum := md5.Sum(f.data)
	f.md5hex = hex.EncodeToString(sum[:])
	if s.AsyncMultipartChecksum {
		// The provider stores the multipart ETag placeholder and recomputes
		// the real checksum in a background task (DOCUMENTED-only, D44).
		f.checksum = fmt.Sprintf("multipart:%s-%d", f.md5hex[:8], f.partSize)
	} else {
		f.checksum = "md5:" + f.md5hex
	}
	f.committed = true
	f.transfer = "L"
	writeJSON(w, 200, s.fileJSON(rec, f))
}

func (s *Server) publish(w http.ResponseWriter, rec *record) {
	// Validation errors arrive as {status, message, errors[]} (fixture 05).
	var errs []map[string]any
	if rec.meta == nil || rec.meta["publisher"] == nil || rec.meta["publisher"] == "" {
		errs = append(errs, map[string]any{
			"field":    "metadata.publisher",
			"messages": []string{"Missing publisher field required for DOI registration."},
		})
	}
	for _, f := range rec.files {
		if !f.committed {
			errs = append(errs, map[string]any{
				"field":    "files",
				"messages": []string{"One or more files have not completed their transfer, please wait."},
			})
			break
		}
	}
	if len(errs) > 0 {
		writeJSON(w, 400, map[string]any{
			"status": 400, "message": "A validation error occurred.", "errors": errs,
		})
		return
	}

	doPublish := func() {
		rec.published = true
		rec.doi = "10.5072/zenodo." + rec.id
		rec.conceptDOI = "10.5072/zenodo." + rec.conceptID
	}

	if s.FailPublish != nil {
		if status, ok := s.FailPublish[rec.id]; ok {
			delete(s.FailPublish, rec.id)
			if status >= 500 {
				// The zenodo#2131 divergence: the action reports failure
				// while the publish succeeds server-side.
				doPublish()
			}
			jsonError(w, status, "Injected failure.")
			return
		}
	}
	doPublish()
	writeJSON(w, 202, s.recordJSON(rec, false))
}

func (s *Server) filesImport(w http.ResponseWriter, rec *record) {
	if len(rec.files) > 0 {
		writeJSON(w, 400, map[string]any{
			"status": 400, "message": "A validation error occurred.",
			"errors": []map[string]any{{"field": "files.enabled", "messages": []string{"Please remove all files first."}}},
		})
		return
	}
	chain := s.concepts[rec.conceptID]
	var prev *record
	for _, rid := range chain {
		if rid != rec.id && s.records[rid] != nil && s.records[rid].published {
			prev = s.records[rid]
		}
	}
	if prev == nil {
		jsonError(w, 400, "No published files to import.")
		return
	}
	for _, k := range prev.fileOrder {
		pf := prev.files[k]
		rec.files[k] = &file{key: k, data: pf.data, committed: true, md5hex: pf.md5hex, checksum: "md5:" + pf.md5hex, transfer: "L", size: pf.size}
		rec.fileOrder = append(rec.fileOrder, k)
	}
	writeJSON(w, 201, s.filesJSON(rec))
}

func (s *Server) newVersion(w http.ResponseWriter, rec *record) {
	chain := s.concepts[rec.conceptID]
	// Idempotent: an existing version draft is returned as-is (fixture 11).
	for _, rid := range chain {
		if r2 := s.records[rid]; r2 != nil && !r2.published {
			writeJSON(w, 201, s.recordJSON(r2, true))
			return
		}
	}
	id := s.allocID()
	draft := &record{
		id: id, conceptID: rec.conceptID, index: len(chain),
		meta: cloneMeta(s.latestPublished(rec.conceptID).meta), files: map[string]*file{},
	}
	s.records[id] = draft
	s.concepts[rec.conceptID] = append(chain, id)
	writeJSON(w, 201, s.recordJSON(draft, true))
}

func (s *Server) versionsList(w http.ResponseWriter, rec *record) {
	chain := s.concepts[rec.conceptID]
	var hits []any
	for i := len(chain) - 1; i >= 0; i-- { // newest first (fixture 40)
		r2 := s.records[chain[i]]
		if r2 != nil && r2.published {
			hits = append(hits, s.recordJSON(r2, false))
		}
	}
	if hits == nil {
		hits = []any{}
	}
	writeJSON(w, 200, map[string]any{"hits": map[string]any{"hits": hits, "total": len(hits)}})
}

func (s *Server) userRecords(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := r.URL.Query().Get("q")
	draftsOnly := strings.Contains(q, "is_published:false")

	var hits []any
	var conceptIDs []string
	for cid := range s.concepts {
		conceptIDs = append(conceptIDs, cid)
	}
	sort.Strings(conceptIDs)
	for _, cid := range conceptIDs {
		chain := s.concepts[cid]
		if len(chain) == 0 {
			continue
		}
		latest := s.records[chain[len(chain)-1]]
		if latest == nil {
			continue
		}
		if draftsOnly && latest.published {
			continue
		}
		j := s.recordJSON(latest, !latest.published)
		j["status"] = map[bool]string{true: "published", false: "draft"}[latest.published]
		hits = append(hits, j)
	}
	if hits == nil {
		hits = []any{}
	}
	writeJSON(w, 200, map[string]any{"hits": map[string]any{"hits": hits, "total": len(hits)}})
}

func (s *Server) download(w http.ResponseWriter, rec *record, key string) {
	if !rec.published {
		jsonError(w, 404, "Not found.")
		return
	}
	f, ok := rec.files[key]
	if !ok {
		jsonError(w, 404, "Not found.")
		return
	}
	// Downloads carry oc-checksum, not Content-MD5, and real Zenodo
	// strips leading zeros from the hex (live-tier finding) — mirror it.
	w.Header().Set("oc-checksum", "MD5:"+strings.TrimLeft(f.md5hex, "0"))
	w.Header().Set("Content-Length", strconv.Itoa(len(f.data)))
	w.WriteHeader(200)
	_, _ = w.Write(f.data)
}

// --- serialization (hybrid legacy/RDM shapes, D17/D19) ---

// recordJSON renders the Zenodo-sandbox hybrid: numeric id, top-level
// doi/conceptdoi when minted, RDM links, legacy relations block.
func (s *Server) recordJSON(rec *record, draft bool) map[string]any {
	base := s.ts.URL + "/api/records/" + rec.id
	self := base
	if draft {
		self = base + "/draft"
	}
	idNum, _ := strconv.Atoi(rec.id)
	j := map[string]any{
		"id":           idNum,
		"conceptrecid": rec.conceptID,
		"metadata": mergeMeta(rec.meta, map[string]any{
			"relations": map[string]any{"version": []any{map[string]any{
				"index": rec.index, "is_last": s.isLast(rec),
				"parent": map[string]any{"pid_type": "recid", "pid_value": rec.conceptID},
			}}},
		}),
		"links": map[string]any{
			"self":     self,
			"files":    self + "/files",
			"versions": base + "/versions",
			"publish":  base + "/draft/actions/publish",
			"record":   base,
			"latest":   base + "/versions/latest",
		},
	}
	if rec.doi != "" {
		j["doi"] = rec.doi
		j["conceptdoi"] = rec.conceptDOI
	} else if rec.reservedDOI != "" {
		j["doi"] = rec.reservedDOI
	}
	if rec.published {
		j["state"] = "done"
		j["submitted"] = true
	}
	return j
}

func (s *Server) filesJSON(rec *record) map[string]any {
	entries := []any{}
	for _, k := range rec.fileOrder {
		entries = append(entries, s.fileJSON(rec, rec.files[k]))
	}
	return map[string]any{"enabled": true, "entries": entries, "default_preview": nil, "order": []any{}}
}

func (s *Server) fileJSON(rec *record, f *file) map[string]any {
	self := s.ts.URL + "/api/records/" + rec.id + "/draft/files/" + f.key
	j := map[string]any{
		"key":      f.key,
		"status":   map[bool]string{true: "completed", false: "pending"}[f.committed],
		"transfer": map[string]any{"type": f.transfer},
		"links": map[string]any{
			"self": self, "content": self + "/content", "commit": self + "/commit",
		},
	}
	if f.committed {
		j["checksum"] = f.checksum
		j["size"] = f.size
	}
	if f.transfer == "M" {
		// Fixture 39: per-part upload URLs with ~14-day expirations.
		var parts []any
		for i := 1; i <= f.parts; i++ {
			parts = append(parts, map[string]any{
				"part":       i,
				"url":        self + "/content/" + strconv.Itoa(i),
				"expiration": "2026-08-24T23:38:00.765560+00:00",
			})
		}
		j["links"].(map[string]any)["parts"] = parts
	}
	return j
}

// --- helpers ---

func (s *Server) authorized(r *http.Request) bool {
	// Published-record reads work anonymously (A1 — anonymous read is
	// sacred); everything else needs the bearer token.
	if r.Method == "GET" && !strings.Contains(r.URL.Path, "/draft") && !strings.HasPrefix(r.URL.Path, "/api/user/") {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+s.token
}

func (s *Server) allocID() string {
	s.nextID++
	return strconv.Itoa(s.nextID)
}

func (s *Server) isLast(rec *record) bool {
	chain := s.concepts[rec.conceptID]
	return len(chain) > 0 && chain[len(chain)-1] == rec.id
}

func (s *Server) latestPublished(conceptID string) *record {
	var latest *record
	for _, rid := range s.concepts[conceptID] {
		if r := s.records[rid]; r != nil && r.published {
			latest = r
		}
	}
	return latest
}

func cloneMeta(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mergeMeta(meta, extra map[string]any) map[string]any {
	out := cloneMeta(meta)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"status": status, "message": msg})
}
