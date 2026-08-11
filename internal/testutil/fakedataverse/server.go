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
package fakedataverse

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
}

type dataset struct {
	id        int
	pid       string
	meta      map[string]any
	draft     map[int]*file // current draft (nil when no draft)
	published []version
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
	}
	s.ts = httptest.NewServer(http.HandlerFunc(s.route))
	return s
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

	// Anonymous: info + published reads + downloads.
	switch {
	case parts[0] == "info":
		ok(w, map[string]any{"version": "6.3", "build": "fake"})
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
		ok(w, s.datasetJSON(d))
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

func (s *Server) addFile(w http.ResponseWriter, r *http.Request, d *dataset) {
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
	if jd := r.FormValue("jsonData"); jd != "" {
		var meta struct {
			DirectoryLabel string `json:"directoryLabel"`
		}
		if err := json.Unmarshal([]byte(jd), &meta); err == nil {
			dir = meta.DirectoryLabel
		}
	}
	for _, f := range d.draft {
		if f.label == hdr.Filename && f.dir == dir {
			fail(w, 400, "a file with this name already exists at this path")
			return
		}
	}
	sum := md5.Sum(data)
	s.nextID++
	f := &file{id: s.nextID, label: hdr.Filename, dir: dir, data: data, md5: hex.EncodeToString(sum[:])}
	d.draft[f.id] = f
	s.files[f.id] = f
	ok(w, map[string]any{"files": []any{map[string]any{
		"label": f.label, "directoryLabel": f.dir,
		"dataFile": map[string]any{"id": f.id, "filename": f.label, "md5": f.md5, "filesize": len(f.data)},
	}}})
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request, d *dataset) {
	if r.URL.Query().Get("type") != "major" {
		fail(w, 400, "only type=major is modeled")
		return
	}
	if d.draft == nil {
		fail(w, 400, "no draft to publish")
		return
	}
	files := d.draft
	d.draft = nil
	d.published = append(d.published, version{number: len(d.published) + 1, files: files})
	ok(w, map[string]any{"id": d.id, "persistentId": d.pid})
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
			// A released dataset with no open draft: the draft view is the
			// latest released version (matches Dataverse's lazy drafts).
			if len(d.published) == 0 {
				fail(w, 404, "no draft")
				return
			}
			files = d.published[len(d.published)-1].files
		} else {
			files = d.draft
		}
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
