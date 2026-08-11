// Package fakefigshare is a hermetic, in-process fake of the Figshare v2
// API (docs.figshare.com) for the figshare adapter's tests.
//
// ⚠ Unlike fakeinvenio, this fake encodes DOCUMENTED behavior, not
// live-verified behavior — no Figshare credentials were available when it
// was written (see docs/decisions.md D28). Behaviors modeled:
//
//   - drafts are "account articles"; a published article stays editable
//     and re-publishing mints the next .vN version DOI
//   - the parted upload flow: initiate (name/size/md5) → file info with
//     upload_token/upload_url → parts listing → PUT parts → complete,
//     with the server verifying the supplied MD5
//   - reserve_doi; version listing via /articles/{id}/versions/{v}
//   - datasets must carry at least one available file to publish
package fakefigshare

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

// Server is the fake. Create with New.
type Server struct {
	ts    *httptest.Server
	token string

	mu       sync.Mutex
	nextID   int
	articles map[int]*article
}

type article struct {
	id        int
	meta      map[string]any
	files     map[int]*file
	fileOrder []int
	published int    // number of published versions (0 = never)
	doi       string // base DOI once reserved/published
	versions  []versionSnapshot
	deleted   bool
}

type file struct {
	id          int
	name        string
	size        int64
	suppliedMD5 string
	data        []byte
	status      string // created | available
}

type versionSnapshot struct {
	files []fileSnapshot
}

type fileSnapshot struct {
	id   int
	name string
	size int64
	md5  string
	data []byte
}

// New starts the fake accepting the given personal access token.
func New(token string) *Server {
	s := &Server{token: token, nextID: 9000000, articles: map[int]*article{}}
	s.ts = httptest.NewServer(http.HandlerFunc(s.route))
	return s
}

// URL returns the API base (stands in for https://api.figshare.com).
func (s *Server) URL() string { return s.ts.URL }

// Close shuts the server down.
func (s *Server) Close() { s.ts.Close() }

// PublishedCount reports how many articles have at least one published
// version (for no-publish assertions).
func (s *Server) PublishedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, a := range s.articles {
		if a.published > 0 {
			n++
		}
	}
	return n
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/v2")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")

	// Public (anonymous) article reads.
	if parts[0] == "articles" {
		s.publicRoute(w, r, parts[1:])
		return
	}
	// Upload-service endpoints (real Figshare hosts these separately).
	if parts[0] == "upload" {
		s.uploadRoute(w, r, parts[1:])
		return
	}
	if parts[0] == "download" {
		s.downloadRoute(w, r, parts[1:])
		return
	}

	if r.Header.Get("Authorization") != "token "+s.token {
		jsonError(w, 403, "Invalid token")
		return
	}
	switch {
	case parts[0] == "account" && len(parts) >= 2 && parts[1] == "articles":
		s.accountArticles(w, r, parts[2:])
	case parts[0] == "account" && len(parts) == 2 && parts[1] == "licenses":
		writeJSON(w, 200, []any{
			map[string]any{"value": 1, "name": "CC BY 4.0", "url": "https://creativecommons.org/licenses/by/4.0/"},
			map[string]any{"value": 2, "name": "CC0", "url": "https://creativecommons.org/publicdomain/zero/1.0/"},
			map[string]any{"value": 3, "name": "MIT", "url": "https://opensource.org/licenses/MIT"},
		})
	default:
		jsonError(w, 404, "Not found")
	}
}

func (s *Server) accountArticles(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method != "POST" {
			jsonError(w, 405, "Method not allowed")
			return
		}
		var meta map[string]any
		if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
			jsonError(w, 400, "Malformed request")
			return
		}
		s.nextID++
		a := &article{id: s.nextID, meta: meta, files: map[int]*file{}}
		s.articles[a.id] = a
		writeJSON(w, 201, map[string]any{
			"location":   s.ts.URL + "/v2/account/articles/" + strconv.Itoa(a.id),
			"entity_id":  a.id,
			"warnings":   []string{},
			"entity_url": s.ts.URL + "/v2/account/articles/" + strconv.Itoa(a.id),
		})
		return
	}

	id, err := strconv.Atoi(rest[0])
	a := s.articles[id]
	if err != nil || a == nil || a.deleted {
		jsonError(w, 404, "Article not found")
		return
	}
	rest = rest[1:]

	switch {
	case len(rest) == 0 && r.Method == "GET":
		writeJSON(w, 200, s.draftJSON(a))
	case len(rest) == 0 && r.Method == "PUT":
		var meta map[string]any
		if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
			jsonError(w, 400, "Malformed request")
			return
		}
		for k, v := range meta {
			a.meta[k] = v
		}
		writeJSON(w, 205, map[string]any{})
	case len(rest) == 0 && r.Method == "DELETE":
		if a.published > 0 {
			jsonError(w, 403, "Published articles cannot be deleted")
			return
		}
		a.deleted = true
		w.WriteHeader(204)
	case len(rest) == 1 && rest[0] == "reserve_doi" && r.Method == "POST":
		if a.doi == "" {
			a.doi = fmt.Sprintf("10.5072/fk.figshare.%d", a.id)
		}
		writeJSON(w, 200, map[string]any{"doi": a.doi})
	case len(rest) == 1 && rest[0] == "publish" && r.Method == "POST":
		s.publish(w, a)
	case len(rest) >= 1 && rest[0] == "files":
		s.draftFiles(w, r, a, rest[1:])
	default:
		jsonError(w, 404, "Not found")
	}
}

func (s *Server) publish(w http.ResponseWriter, a *article) {
	available := 0
	for _, f := range a.files {
		if f.status == "available" {
			available++
		} else {
			jsonError(w, 400, "One or more files are still being processed")
			return
		}
	}
	if available == 0 {
		jsonError(w, 400, "A dataset must contain at least one file to be published")
		return
	}
	if a.meta["title"] == nil || a.meta["title"] == "" {
		jsonError(w, 400, "Missing mandatory field: title")
		return
	}
	if a.doi == "" {
		a.doi = fmt.Sprintf("10.5072/fk.figshare.%d", a.id)
	}
	snap := versionSnapshot{}
	for _, fid := range a.fileOrder {
		f := a.files[fid]
		snap.files = append(snap.files, fileSnapshot{id: f.id, name: f.name, size: f.size, md5: f.suppliedMD5, data: f.data})
	}
	a.versions = append(a.versions, snap)
	a.published = len(a.versions)
	writeJSON(w, 201, map[string]any{
		"location": s.ts.URL + "/v2/articles/" + strconv.Itoa(a.id),
	})
}

func (s *Server) draftFiles(w http.ResponseWriter, r *http.Request, a *article, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == "POST":
		var req struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			MD5  string `json:"md5"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, 400, "Malformed request")
			return
		}
		for _, f := range a.files {
			if f.name == req.Name {
				jsonError(w, 400, "A file with this name already exists")
				return
			}
		}
		s.nextID++
		f := &file{id: s.nextID, name: req.Name, size: req.Size, suppliedMD5: strings.ToLower(req.MD5), status: "created"}
		a.files[f.id] = f
		a.fileOrder = append(a.fileOrder, f.id)
		writeJSON(w, 201, map[string]any{
			"location": s.ts.URL + "/v2/account/articles/" + strconv.Itoa(a.id) + "/files/" + strconv.Itoa(f.id),
		})
	case len(rest) == 0 && r.Method == "GET":
		var out []any
		for _, fid := range a.fileOrder {
			out = append(out, s.fileJSON(a, a.files[fid]))
		}
		if out == nil {
			out = []any{}
		}
		writeJSON(w, 200, out)
	case len(rest) == 1:
		fid, _ := strconv.Atoi(rest[0])
		f := a.files[fid]
		if f == nil {
			jsonError(w, 404, "File not found")
			return
		}
		switch r.Method {
		case "GET":
			writeJSON(w, 200, s.fileJSON(a, f))
		case "DELETE":
			delete(a.files, fid)
			for i, id := range a.fileOrder {
				if id == fid {
					a.fileOrder = append(a.fileOrder[:i], a.fileOrder[i+1:]...)
					break
				}
			}
			w.WriteHeader(204)
		case "POST": // complete upload
			computed := md5hex(f.data)
			if computed != f.suppliedMD5 {
				jsonError(w, 400, "MD5 mismatch: supplied "+f.suppliedMD5+" computed "+computed)
				return
			}
			f.status = "available"
			w.WriteHeader(202)
		default:
			jsonError(w, 405, "Method not allowed")
		}
	default:
		jsonError(w, 404, "Not found")
	}
}

// uploadRoute is the parted upload service: GET parts, PUT one part.
func (s *Server) uploadRoute(w http.ResponseWriter, r *http.Request, rest []string) {
	// /upload/{articleID}/{fileID}[/{part}]
	if len(rest) < 2 {
		jsonError(w, 404, "Not found")
		return
	}
	aid, _ := strconv.Atoi(rest[0])
	fid, _ := strconv.Atoi(rest[1])
	a := s.articles[aid]
	if a == nil {
		jsonError(w, 404, "Not found")
		return
	}
	f := a.files[fid]
	if f == nil {
		jsonError(w, 404, "Not found")
		return
	}
	switch {
	case len(rest) == 2 && r.Method == "GET":
		// One part is enough for the fake — the driver must follow the
		// parts list rather than assume a shape.
		writeJSON(w, 200, map[string]any{
			"token": fmt.Sprintf("tok-%d", fid),
			"parts": []any{map[string]any{
				"partNo": 1, "startOffset": 0, "endOffset": f.size - 1, "status": "PENDING",
			}},
		})
	case len(rest) == 3 && r.Method == "PUT":
		data, err := io.ReadAll(r.Body)
		if err != nil {
			jsonError(w, 400, "Malformed body")
			return
		}
		f.data = append(f.data, data...)
		w.WriteHeader(200)
	default:
		jsonError(w, 405, "Method not allowed")
	}
}

func (s *Server) downloadRoute(w http.ResponseWriter, r *http.Request, rest []string) {
	// /download/{articleID}/{version}/{fileID}
	if len(rest) != 3 {
		jsonError(w, 404, "Not found")
		return
	}
	aid, _ := strconv.Atoi(rest[0])
	ver, _ := strconv.Atoi(rest[1])
	fid, _ := strconv.Atoi(rest[2])
	a := s.articles[aid]
	if a == nil || ver < 1 || ver > len(a.versions) {
		jsonError(w, 404, "Not found")
		return
	}
	for _, f := range a.versions[ver-1].files {
		if f.id == fid {
			w.Header().Set("Content-Length", strconv.Itoa(len(f.data)))
			w.WriteHeader(200)
			_, _ = w.Write(f.data)
			return
		}
	}
	jsonError(w, 404, "Not found")
}

func (s *Server) publicRoute(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 || r.Method != "GET" {
		jsonError(w, 404, "Not found")
		return
	}
	id, err := strconv.Atoi(rest[0])
	a := s.articles[id]
	if err != nil || a == nil || a.deleted || a.published == 0 {
		jsonError(w, 404, "Article not found")
		return
	}
	rest = rest[1:]
	switch {
	case len(rest) == 0:
		writeJSON(w, 200, s.publicJSON(a, a.published))
	case len(rest) == 1 && rest[0] == "versions":
		var out []any
		for v := 1; v <= a.published; v++ {
			out = append(out, map[string]any{
				"version": v,
				"url":     s.ts.URL + "/v2/articles/" + strconv.Itoa(a.id) + "/versions/" + strconv.Itoa(v),
			})
		}
		writeJSON(w, 200, out)
	case len(rest) == 2 && rest[0] == "versions":
		v, _ := strconv.Atoi(rest[1])
		if v < 1 || v > a.published {
			jsonError(w, 404, "Version not found")
			return
		}
		writeJSON(w, 200, s.publicJSON(a, v))
	case len(rest) == 1 && rest[0] == "files":
		writeJSON(w, 200, s.publicFilesJSON(a, a.published))
	default:
		jsonError(w, 404, "Not found")
	}
}

// --- serialization ---

func (s *Server) draftJSON(a *article) map[string]any {
	j := map[string]any{
		"id":     a.id,
		"title":  a.meta["title"],
		"doi":    a.doi,
		"status": map[bool]string{true: "public", false: "draft"}[a.published > 0],
	}
	var files []any
	for _, fid := range a.fileOrder {
		files = append(files, s.fileJSON(a, a.files[fid]))
	}
	if files == nil {
		files = []any{}
	}
	j["files"] = files
	return j
}

func (s *Server) fileJSON(a *article, f *file) map[string]any {
	return map[string]any{
		"id":           f.id,
		"name":         f.name,
		"size":         f.size,
		"supplied_md5": f.suppliedMD5,
		"computed_md5": md5hex(f.data),
		"status":       f.status,
		"upload_token": fmt.Sprintf("tok-%d", f.id),
		"upload_url":   s.ts.URL + "/v2/upload/" + strconv.Itoa(a.id) + "/" + strconv.Itoa(f.id),
		"download_url": s.ts.URL + "/v2/download/" + strconv.Itoa(a.id) + "/" + strconv.Itoa(len(a.versions)+1) + "/" + strconv.Itoa(f.id),
	}
}

// publicJSON renders version v (1-based) of a published article. The
// version DOI is base.vN; the base DOI resolves to the latest version.
func (s *Server) publicJSON(a *article, v int) map[string]any {
	return map[string]any{
		"id":             a.id,
		"title":          a.meta["title"],
		"doi":            fmt.Sprintf("%s.v%d", a.doi, v),
		"version":        v,
		"files":          s.publicFilesJSON(a, v),
		"url_public":     s.ts.URL + "/v2/articles/" + strconv.Itoa(a.id),
		"defined_type":   3,
		"is_embargoed":   false,
		"published_date": "2026-08-11T00:00:00Z",
	}
}

func (s *Server) publicFilesJSON(a *article, v int) []any {
	var out []any
	snap := a.versions[v-1]
	files := append([]fileSnapshot(nil), snap.files...)
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	for _, f := range files {
		out = append(out, map[string]any{
			"id":           f.id,
			"name":         f.name,
			"size":         f.size,
			"computed_md5": f.md5,
			"download_url": s.ts.URL + "/v2/download/" + strconv.Itoa(a.id) + "/" + strconv.Itoa(v) + "/" + strconv.Itoa(f.id),
		})
	}
	if out == nil {
		out = []any{}
	}
	return out
}

func md5hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"message": msg, "code": status})
}
