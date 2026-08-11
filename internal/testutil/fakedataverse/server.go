// Package fakedataverse is a hermetic, in-process fake of the Dataverse
// native API (guides.dataverse.org) for the dataverse adapter's tests.
//
// ⚠ Encodes DOCUMENTED behavior, not live-verified behavior — no
// Dataverse instance was available when it was written (D28). Modeled:
//
//   - datasets created under a collection with the DOI (persistentId)
//     reserved at creation
//   - multipart /add uploads with directoryLabel for path-ish keys and
//     server-computed MD5s
//   - the draft version accumulating changes on a published dataset;
//     :publish?type=major releases it as the next x.0 version
//   - one DOI for all versions (no per-version DOIs)
//   - the {status, data|message} response envelope
//   - the per-instance license registry (/api/licenses) and rejection of
//     unregistered license names on dataset creation — LIVE-VERIFIED
//     against demo.dataverse.org 6.11 (2026-08-11): a raw SPDX id fails
//     with "Error parsing Json: Invalid or unsupported license: …", and
//     the registry's CC0 entry carries the SPDX rightsIdentifier
//     crosswalk while the CC BY entries do not
package fakedataverse

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"sync"
)

// Server is the fake. Create with New.
type Server struct {
	ts    *httptest.Server
	token string

	mu       sync.Mutex
	nextID   int
	datasets map[string]*dataset // by persistentId
	byID     map[int]*dataset
	files    map[int]*file // global file id space

	// publishFinalizePolls is how many dataset GETs a publish takes to
	// finalize (asynchronous publication). Negative = never finalizes.
	publishFinalizePolls int
}

type dataset struct {
	id        int
	pid       string
	meta      map[string]any
	draft     map[int]*file // current draft (nil when no draft)
	published []version

	// lockType is the active lock ("" = unlocked). lockPollsLeft is how
	// many GET /locks calls remain before it auto-clears; negative means
	// it never clears.
	lockType      string
	lockPollsLeft int

	// finalizing models asynchronous publish (live-verified: the publish
	// POST is accepted but the version stays unreleased until DOI
	// finalization completes). finalizeLeft is how many dataset GETs
	// remain before the release lands.
	finalizing   bool
	finalizeLeft int
}

type version struct {
	number int // major, x.0
	files  map[int]*file
}

type file struct {
	id    int
	label string // filename
	dir   string // directoryLabel
	data  []byte
	md5   string
}

// New starts the fake accepting the given API token.
func New(token string) *Server {
	s := &Server{
		token: token, nextID: 5000,
		datasets: map[string]*dataset{}, byID: map[int]*dataset{}, files: map[int]*file{},
		publishFinalizePolls: 2,
	}
	s.ts = httptest.NewServer(http.HandlerFunc(s.route))
	return s
}

// SetPublishFinalizePolls tunes how many dataset GETs a publish takes to
// finalize (test hook). Negative = the publication never finalizes.
func (s *Server) SetPublishFinalizePolls(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishFinalizePolls = n
}

// URL returns the instance base URL.
func (s *Server) URL() string { return s.ts.URL }

// Close shuts the server down.
func (s *Server) Close() { s.ts.Close() }

// PublishedCount reports datasets with at least one released version.
func (s *Server) PublishedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.datasets {
		if len(d.published) > 0 {
			n++
		}
	}
	return n
}

// licenseRegistry mirrors demo.dataverse.org's /api/licenses shape: the
// CC0 entry carries the SPDX rightsIdentifier crosswalk, the CC BY
// entries do not (matching the live instance, where only some entries
// have it).
var licenseRegistry = []map[string]any{
	{
		"id": 1, "name": "CC0 1.0",
		"uri":    "http://creativecommons.org/publicdomain/zero/1.0",
		"active": true, "isDefault": true,
		"rightsIdentifier": "CC0-1.0", "rightsIdentifierScheme": "SPDX",
	},
	{
		"id": 13, "name": "CC BY 4.0",
		"uri":    "http://creativecommons.org/licenses/by/4.0",
		"active": true, "isDefault": false,
	},
	{
		"id": 9, "name": "CC BY-SA 4.0",
		"uri":    "http://creativecommons.org/licenses/by-sa/4.0",
		"active": true, "isDefault": false,
	},
}

// SetLock locks a dataset (test hook). clearAfterPolls is how many
// GET /locks calls it survives; negative = never clears.
func (s *Server) SetLock(pid, lockType string, clearAfterPolls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.datasets[pid]; d != nil {
		d.lockType = lockType
		d.lockPollsLeft = clearAfterPolls
	}
}

// tabularExt reports whether a filename would trigger Dataverse's
// asynchronous tabular ingest.
func tabularExt(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".csv", ".tsv", ".tab", ".xlsx", ".dta", ".sav", ".por":
		return true
	}
	return false
}

// contactEmail digs the first datasetContactEmail value out of a dataset
// creation body ("" when absent).
func contactEmail(body map[string]any) string {
	dv, _ := body["datasetVersion"].(map[string]any)
	blocks, _ := dv["metadataBlocks"].(map[string]any)
	citation, _ := blocks["citation"].(map[string]any)
	fields, _ := citation["fields"].([]any)
	for _, f := range fields {
		fm, _ := f.(map[string]any)
		if fm["typeName"] != "datasetContact" {
			continue
		}
		contacts, _ := fm["value"].([]any)
		for _, ct := range contacts {
			cm, _ := ct.(map[string]any)
			email, _ := cm["datasetContactEmail"].(map[string]any)
			if v, _ := email["value"].(string); v != "" {
				return v
			}
		}
	}
	return ""
}

// DatasetContactEmail returns the contact e-mail a dataset was created
// with (assertion hook for driver tests).
func (s *Server) DatasetContactEmail(pid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.datasets[pid]
	if d == nil {
		return ""
	}
	return contactEmail(d.meta)
}

// DatasetLicense returns the license object a dataset was created with,
// nil when none was sent (assertion hook for driver tests).
func (s *Server) DatasetLicense(pid string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.datasets[pid]
	if d == nil {
		return nil
	}
	dv, _ := d.meta["datasetVersion"].(map[string]any)
	lic, _ := dv["license"].(map[string]any)
	return lic
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, 200, map[string]any{"status": "OK", "data": data})
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"status": "ERROR", "message": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := strings.TrimSuffix(r.URL.Path, "/")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" {
		fail(w, 404, "not found")
		return
	}
	parts = parts[1:]

	// Anonymous: info + licenses + published reads + downloads.
	switch {
	case parts[0] == "info":
		ok(w, map[string]any{"version": "6.3", "build": "fake"})
		return
	case parts[0] == "licenses" && len(parts) == 1 && r.Method == "GET":
		ok(w, licenseRegistry)
		return
	case parts[0] == "access" && len(parts) == 3 && parts[1] == "datafile":
		s.download(w, parts[2])
		return
	}

	if r.Header.Get("X-Dataverse-key") != s.token {
		fail(w, 401, "Bad API key")
		return
	}

	switch {
	case parts[0] == "dataverses" && len(parts) == 3 && parts[2] == "datasets" && r.Method == "POST":
		s.createDataset(w, r)
	case parts[0] == "datasets" && len(parts) >= 2 && parts[1] == ":persistentId":
		s.datasetRoute(w, r, parts[2:])
	case parts[0] == "datasets" && len(parts) == 2 && r.Method == "DELETE":
		s.destroyDataset(w, parts[1])
	case parts[0] == "files" && len(parts) == 2 && r.Method == "DELETE":
		s.deleteFile(w, parts[1])
	default:
		fail(w, 404, "not found")
	}
}

func (s *Server) createDataset(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "malformed dataset JSON")
		return
	}
	if msg := validateLicense(body); msg != "" {
		fail(w, 400, msg)
		return
	}
	// Live-verified (demo 6.11): dataset creation without a Point of
	// Contact e-mail fails with HTTP 403.
	if contactEmail(body) == "" {
		fail(w, 403, "Validation Failed: Point of Contact E-mail is required.")
		return
	}
	s.nextID++
	d := &dataset{
		id:    s.nextID,
		pid:   fmt.Sprintf("doi:10.5072/FK2/FAKE%d", s.nextID),
		meta:  body,
		draft: map[int]*file{},
	}
	s.datasets[d.pid] = d
	s.byID[d.id] = d
	ok(w, map[string]any{"id": d.id, "persistentId": d.pid})
}

// validateLicense enforces the registry the way real Dataverse does
// (live-verified error text): a license, when present, must name a
// registered entry exactly.
func validateLicense(body map[string]any) string {
	dv, _ := body["datasetVersion"].(map[string]any)
	if dv == nil {
		return ""
	}
	lic, present := dv["license"]
	if !present {
		return ""
	}
	m, isMap := lic.(map[string]any)
	if !isMap {
		return fmt.Sprintf("Error parsing Json: Invalid or unsupported license: %v", lic)
	}
	name, _ := m["name"].(string)
	for _, entry := range licenseRegistry {
		if entry["name"] == name && entry["active"] == true {
			if uri, ok := m["uri"].(string); ok && uri != "" && uri != entry["uri"] {
				return fmt.Sprintf("Error parsing Json: Invalid or unsupported license: %s", uri)
			}
			return ""
		}
	}
	return fmt.Sprintf("Error parsing Json: Invalid or unsupported license: %s", name)
}

func (s *Server) destroyDataset(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	d := s.byID[id]
	if d == nil {
		fail(w, 404, "dataset not found")
		return
	}
	if len(d.published) > 0 {
		fail(w, 403, "published datasets cannot be deleted")
		return
	}
	delete(s.datasets, d.pid)
	delete(s.byID, d.id)
	ok(w, map[string]any{"message": "deleted"})
}

func (s *Server) deleteFile(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	for _, d := range s.datasets {
		if d.draft != nil {
			if _, in := d.draft[id]; in {
				delete(d.draft, id)
				ok(w, map[string]any{"message": "deleted"})
				return
			}
			continue
		}
		// Lazy drafts: deleting a file of a released dataset opens its
		// draft (seeded from the latest version) and removes it there.
		if len(d.published) > 0 {
			latest := d.published[len(d.published)-1].files
			if _, in := latest[id]; in {
				d.draft = map[int]*file{}
				for fid, f := range latest {
					if fid != id {
						d.draft[fid] = f
					}
				}
				ok(w, map[string]any{"message": "deleted"})
				return
			}
		}
	}
	fail(w, 404, "file not found in any draft")
}

func (s *Server) datasetRoute(w http.ResponseWriter, r *http.Request, rest []string) {
	pid := r.URL.Query().Get("persistentId")
	d := s.datasets[pid]
	if d == nil {
		fail(w, 404, "dataset not found")
		return
	}
	switch {
	case len(rest) == 0 && r.Method == "GET":
		s.maybeFinalize(d, 1) // each read brings an async publish closer to done
		ok(w, s.datasetJSON(d))
	case len(rest) == 1 && rest[0] == "locks" && r.Method == "GET":
		s.locks(w, d)
	case len(rest) == 1 && rest[0] == "add" && r.Method == "POST":
		s.addFile(w, r, d)
	case len(rest) == 1 && rest[0] == "versions" && r.Method == "GET":
		s.versionsList(w, d)
	case len(rest) == 3 && rest[0] == "versions" && rest[2] == "files" && r.Method == "GET":
		s.versionFiles(w, d, rest[1])
	case len(rest) == 2 && rest[0] == "actions" && rest[1] == ":publish" && r.Method == "POST":
		s.publish(w, r, d)
	default:
		fail(w, 404, "not found")
	}
}

// locks lists the dataset's active locks, aging the auto-clear counter —
// each poll of a clearing lock brings it closer to done, modeling
// asynchronous completion deterministically.
func (s *Server) locks(w http.ResponseWriter, d *dataset) {
	if d.lockType == "" {
		ok(w, []any{})
		return
	}
	lock := map[string]any{"lockType": d.lockType, "message": "fake lock"}
	if d.lockPollsLeft > 0 {
		d.lockPollsLeft--
		if d.lockPollsLeft == 0 {
			d.lockType = ""
		}
	}
	ok(w, []any{lock})
}

func lockedMsg(d *dataset) string {
	return "This dataset is locked. Reason: " + d.lockType + ". Please try publishing later."
}

func (s *Server) addFile(w http.ResponseWriter, r *http.Request, d *dataset) {
	if d.lockType != "" {
		fail(w, 403, lockedMsg(d))
		return
	}
	if d.draft == nil {
		// Mutating a released dataset opens its draft, seeded with the
		// latest version's files.
		d.draft = map[int]*file{}
		if len(d.published) > 0 {
			for id, f := range d.published[len(d.published)-1].files {
				d.draft[id] = f
			}
		}
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		fail(w, 400, "malformed multipart request")
		return
	}
	src, hdr, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "no file part")
		return
	}
	defer func() { _ = src.Close() }()
	data, err := io.ReadAll(src)
	if err != nil {
		fail(w, 400, "reading file part")
		return
	}
	dir := ""
	tabIngest := true // Dataverse ingests tabular files unless told not to
	if jd := r.FormValue("jsonData"); jd != "" {
		var meta struct {
			DirectoryLabel string `json:"directoryLabel"`
			TabIngest      *bool  `json:"tabIngest"`
		}
		if err := json.Unmarshal([]byte(jd), &meta); err == nil {
			dir = meta.DirectoryLabel
			if meta.TabIngest != nil {
				tabIngest = *meta.TabIngest
			}
		}
	}
	// LIVE-VERIFIED: Dataverse does NOT reject a duplicate label+dir — it
	// silently renames the incoming file (data.csv → data-1.csv), which is
	// how a replace-gone-wrong published v1's data.csv alongside a renamed
	// v2 upload. Model the rename so drivers must handle it.
	name := hdr.Filename
	for taken, n := true, 0; taken; {
		taken = false
		for _, f := range d.draft {
			if f.label == name && f.dir == dir {
				n++
				ext := path.Ext(hdr.Filename)
				name = strings.TrimSuffix(hdr.Filename, ext) + fmt.Sprintf("-%d", n) + ext
				taken = true
				break
			}
		}
	}
	sum := md5.Sum(data)
	s.nextID++
	f := &file{id: s.nextID, label: name, dir: dir, data: data, md5: hex.EncodeToString(sum[:])}
	d.draft[f.id] = f
	s.files[f.id] = f
	// Live divergence: a tabular upload without "tabIngest": false starts
	// asynchronous ingest, locking the dataset ("This dataset is locked.
	// Reason: Ingest.") — and, worse than the lock, real ingest REWRITES
	// the file (data.csv → data.tab, new checksums), breaking byte
	// fidelity. The fake's lock never clears: the driver must disable
	// ingest, not wait it out.
	if tabularExt(hdr.Filename) && tabIngest {
		d.lockType = "Ingest"
		d.lockPollsLeft = -1
	}
	ok(w, map[string]any{"files": []any{map[string]any{
		"label": f.label, "directoryLabel": f.dir,
		"dataFile": map[string]any{"id": f.id, "filename": f.label, "md5": f.md5, "filesize": len(f.data)},
	}}})
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request, d *dataset) {
	if d.lockType != "" {
		fail(w, 403, lockedMsg(d))
		return
	}
	if r.URL.Query().Get("type") != "major" {
		fail(w, 400, "only type=major is modeled")
		return
	}
	if d.draft == nil {
		fail(w, 400, "no draft to publish")
		return
	}
	// The POST is accepted, but the release lands asynchronously (DOI
	// finalization) — the draft stays a draft and the dataset is locked
	// until maybeFinalize completes it.
	d.finalizing = true
	d.finalizeLeft = s.publishFinalizePolls
	d.lockType = "finalizePublication"
	d.lockPollsLeft = -1 // cleared by finalization, not by lock polls
	s.maybeFinalize(d, 0)
	ok(w, map[string]any{"id": d.id, "persistentId": d.pid})
}

// maybeFinalize ages an in-flight publication by cost polls and completes
// it when the countdown reaches zero (never, when configured negative).
func (s *Server) maybeFinalize(d *dataset, cost int) {
	if !d.finalizing || s.publishFinalizePolls < 0 {
		return
	}
	d.finalizeLeft -= cost
	if d.finalizeLeft > 0 {
		return
	}
	files := d.draft
	d.draft = nil
	d.published = append(d.published, version{number: len(d.published) + 1, files: files})
	d.finalizing = false
	d.lockType = ""
}

func (s *Server) versionsList(w http.ResponseWriter, d *dataset) {
	var out []any
	for _, v := range d.published {
		out = append(out, map[string]any{
			"versionNumber": v.number, "versionMinorNumber": 0, "versionState": "RELEASED",
		})
	}
	if d.draft != nil {
		out = append(out, map[string]any{"versionState": "DRAFT"})
	}
	if out == nil {
		out = []any{}
	}
	ok(w, out)
}

func (s *Server) versionFiles(w http.ResponseWriter, d *dataset, ver string) {
	var files map[int]*file
	switch ver {
	case ":draft":
		if d.draft == nil {
			// LIVE-VERIFIED: there is no readable :draft version until a
			// mutation lazily opens one — a released dataset with no open
			// draft 404s here (the lazy seeding happens on the first /add
			// or file DELETE, not on reads).
			fail(w, 404, "no draft")
			return
		}
		files = d.draft
	case ":latest-published":
		if len(d.published) == 0 {
			fail(w, 404, "not published")
			return
		}
		files = d.published[len(d.published)-1].files
	default:
		n, err := strconv.ParseFloat(ver, 64)
		if err != nil || int(n) < 1 || int(n) > len(d.published) {
			fail(w, 404, "version not found")
			return
		}
		files = d.published[int(n)-1].files
	}
	var out []any
	for _, f := range files {
		out = append(out, map[string]any{
			"label": f.label, "directoryLabel": f.dir,
			"dataFile": map[string]any{"id": f.id, "filename": f.label, "md5": f.md5, "filesize": len(f.data)},
		})
	}
	if out == nil {
		out = []any{}
	}
	ok(w, out)
}

func (s *Server) datasetJSON(d *dataset) map[string]any {
	latest := map[string]any{"versionState": "DRAFT"}
	if len(d.published) > 0 {
		latest = map[string]any{
			"versionNumber": d.published[len(d.published)-1].number,
			"versionState":  "RELEASED",
		}
	}
	return map[string]any{
		"id": d.id, "persistentId": d.pid,
		"latestVersion":   latest,
		"publicationDate": map[bool]any{true: "2026-08-11", false: nil}[len(d.published) > 0],
	}
}

func (s *Server) download(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	f := s.files[id]
	if f == nil {
		fail(w, 404, "datafile not found")
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(f.data)))
	w.WriteHeader(200)
	_, _ = w.Write(f.data)
}
