// Package fakeosf provides a fake OSF + Waterbutler HTTP server for integration tests.
// Both the JSON:API metadata tier (/v2/) and the Waterbutler file tier (/v1/) are
// served by a single httptest.Server so tests only need one URL.
package fakeosf

import (
	"crypto/md5"
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

// UploadRecord describes one upload received by the server.
type UploadRecord struct {
	NodeID  string
	Path    string
	Content []byte
}

type fileVer struct {
	num     int
	content []byte
	md5     string
}

// File is a fake file or folder in OSF Storage.
type File struct {
	ID       string
	Name     string
	FilePath string // full path within OSF Storage, starts with "/"
	IsFolder bool
	Versions []fileVer // oldest-first; latest = last element
	Children []*File
}

// LatestContent returns the content of the newest version, or nil for folders.
func (f *File) LatestContent() []byte {
	if len(f.Versions) == 0 {
		return nil
	}
	return f.Versions[len(f.Versions)-1].content
}

// LatestMD5 returns the MD5 hex string of the newest version.
func (f *File) LatestMD5() string {
	if len(f.Versions) == 0 {
		return ""
	}
	return f.Versions[len(f.Versions)-1].md5
}

// LatestVersion returns the version number of the newest version.
func (f *File) LatestVersion() int {
	if len(f.Versions) == 0 {
		return 0
	}
	return f.Versions[len(f.Versions)-1].num
}

// VersionMD5 returns the MD5 for the given version number, or "" if not found.
func (f *File) VersionMD5(ver int) string {
	for _, v := range f.Versions {
		if v.num == ver {
			return v.md5
		}
	}
	return ""
}

// versionContent returns the content for the given version number, or latest if not found.
func (f *File) versionContent(ver int) []byte {
	for _, v := range f.Versions {
		if v.num == ver {
			return v.content
		}
	}
	return f.LatestContent()
}

type fakeProject struct {
	id           string
	title        string
	description  string
	category     string
	tags         []string
	contributors []fakeContributor
	children     []string // child component node IDs, in insertion order
	root         []*File
	byID         map[string]*File
	byPath       map[string]*File
}

// fakeContributor is one contributor of a fake project. Names mirror the
// embedded user resource real OSF serves on /v2/nodes/{id}/contributors/.
type fakeContributor struct {
	FullName      string
	GivenName     string
	FamilyName    string
	Bibliographic bool
}

// MoveRecord describes one move/copy/rename action received by the server.
type MoveRecord struct {
	FileID   string
	Action   string // "move", "copy", or "rename"
	DestPath string // for move/copy: destination folder
	NewName  string // new filename
	Resource string // cross-project destination node
	Conflict string
}

// NodePatchRecord describes one PATCH /nodes/{id}/ request.
type NodePatchRecord struct {
	NodeID string
	Attrs  map[string]any
}

// Server is a combined fake OSF metadata + Waterbutler HTTP server.
type Server struct {
	srv        *httptest.Server
	projects   map[string]*fakeProject
	allFiles   map[string]*File // all files across projects, keyed by ID
	uploads    []UploadRecord
	deletes    []string          // file IDs that received a DELETE request
	moves      []MoveRecord      // move/copy/rename actions received
	patches    []NodePatchRecord // PATCH /nodes/{id}/ requests received
	folders    []string          // folder paths created via kind=folder PUT
	validToken string            // when set, /v2/users/me/ requires this exact bearer token
	forbidden  map[string]bool   // node IDs that respond 403 (simulate private/no-access)
	versionReq int               // count of GET /v2/files/{id}/versions/ requests
	listReq    int               // count of file-listing requests (pages, not folders)

	wikis              map[string]*WikiPage // all wiki pages across projects, keyed by wiki ID
	wikiOrder          map[string][]string  // per-project wiki IDs, most recently modified first
	wikiDisabled       map[string]bool      // node IDs whose wiki addon is disabled
	wikiDeletes        []string             // wiki IDs that received a DELETE request
	wikiListReq        int                  // count of GET /v2/nodes/{id}/wikis/ requests
	wikiVersionReq     int                  // count of GET /v2/wikis/{id}/versions/ requests
	wikiVersionContent int                  // count of GET /v2/wikis/{id}/versions/{n}/content/ requests

	nextID int
	mu     sync.Mutex
}

// SetForbidden makes node/file reads for nodeID respond 403, simulating a
// private project the caller can't access.
func (s *Server) SetForbidden(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forbidden == nil {
		s.forbidden = map[string]bool{}
	}
	s.forbidden[nodeID] = true
}

// forbid writes a 403 and returns true when nodeID is marked forbidden.
func (s *Server) forbid(w http.ResponseWriter, nodeID string) bool {
	s.mu.Lock()
	f := s.forbidden[nodeID]
	s.mu.Unlock()
	if f {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"errors": []map[string]any{{"detail": "You do not have permission to perform this action."}},
		})
	}
	return f
}

// SetValidToken makes /v2/users/me/ accept only this bearer token (others 401).
// When unset (default), any non-empty bearer token authenticates.
func (s *Server) SetValidToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validToken = token
}

// New creates and starts a new fake server.
func New() *Server {
	s := &Server{
		projects:     make(map[string]*fakeProject),
		allFiles:     make(map[string]*File),
		wikis:        make(map[string]*WikiPage),
		wikiOrder:    make(map[string][]string),
		wikiDisabled: make(map[string]bool),
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handler))
	return s
}

// URL returns the server base URL, used for both DATAPIN_API_BASE and DATAPIN_FILES_BASE.
func (s *Server) URL() string { return s.srv.URL }

// Close shuts the server down.
func (s *Server) Close() { s.srv.Close() }

// AddProject registers a project with no files.
func (s *Server) AddProject(id, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[id] = &fakeProject{
		id: id, title: title,
		byID: make(map[string]*File), byPath: make(map[string]*File),
	}
}

// SetProjectMeta sets a project's description and tags (served on GET
// /v2/nodes/{id}/).
func (s *Server) SetProjectMeta(id, description string, tags ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if proj := s.projects[id]; proj != nil {
		proj.description = description
		proj.tags = tags
	}
}

// AddContributor registers a bibliographic contributor on a project, served
// on GET /v2/nodes/{id}/contributors/ with the user resource embedded.
func (s *Server) AddContributor(projectID, fullName, givenName, familyName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if proj := s.projects[projectID]; proj != nil {
		proj.contributors = append(proj.contributors, fakeContributor{
			FullName: fullName, GivenName: givenName, FamilyName: familyName, Bibliographic: true,
		})
	}
}

// AddComponent registers childID as a component (child node) of parentID.
// The component is a full project of its own — AddFile/AddWiki/etc. work on
// it — and is additionally listed on GET /v2/nodes/{parent}/children/.
func (s *Server) AddComponent(parentID, childID, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[childID] = &fakeProject{
		id: childID, title: title,
		byID: make(map[string]*File), byPath: make(map[string]*File),
	}
	if parent := s.projects[parentID]; parent != nil {
		parent.children = append(parent.children, childID)
	}
}

// AddFile adds a file with a single version to a project.
// filePath must start with "/" (e.g. "/data/results.csv"). Intermediate
// folders are created automatically.
func (s *Server) AddFile(projectID, filePath string, content []byte) *File {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addFileLocked(projectID, filePath, content)
}

// AddVersion appends a new version to an existing file (creating the file if it
// does not exist) and returns it. Test-only helper for building multi-version
// remote states (remote-newer, divergence).
func (s *Server) AddVersion(projectID, filePath string, content []byte) *File {
	s.mu.Lock()
	defer s.mu.Unlock()
	proj := s.projects[projectID]
	f := proj.byPath[filePath]
	if f == nil {
		return s.addFileLocked(projectID, filePath, content)
	}
	h := md5.Sum(content)
	next := f.Versions[len(f.Versions)-1].num + 1
	f.Versions = append(f.Versions, fileVer{num: next, content: content, md5: fmt.Sprintf("%x", h[:])})
	return f
}

// AddFolder ensures a folder (and any parents) exists in a project and returns
// it. Test-only helper for setting up subfolders to push into.
func (s *Server) AddFolder(projectID, folderPath string) *File {
	s.mu.Lock()
	defer s.mu.Unlock()
	proj := s.projects[projectID]
	return s.ensureFolderLocked(proj, folderPath)
}

func (s *Server) addFileLocked(projectID, filePath string, content []byte) *File {
	proj := s.projects[projectID]
	h := md5.Sum(content)
	s.nextID++
	f := &File{
		ID:       fmt.Sprintf("f%d", s.nextID),
		Name:     path.Base(filePath),
		FilePath: filePath,
		Versions: []fileVer{{num: 1, content: content, md5: fmt.Sprintf("%x", h[:])}},
	}
	proj.byID[f.ID] = f
	proj.byPath[filePath] = f
	s.allFiles[f.ID] = f

	parent := path.Dir(filePath)
	if parent == "/" || parent == "." {
		proj.root = append(proj.root, f)
	} else {
		folder := s.ensureFolderLocked(proj, parent)
		folder.Children = append(folder.Children, f)
	}
	return f
}

func (s *Server) ensureFolderLocked(proj *fakeProject, folderPath string) *File {
	if f, ok := proj.byPath[folderPath]; ok {
		return f
	}
	s.nextID++
	folder := &File{
		ID:       fmt.Sprintf("d%d", s.nextID),
		Name:     path.Base(folderPath),
		FilePath: folderPath,
		IsFolder: true,
	}
	proj.byID[folder.ID] = folder
	proj.byPath[folderPath] = folder
	s.allFiles[folder.ID] = folder

	parent := path.Dir(folderPath)
	if parent == "/" || parent == "." {
		proj.root = append(proj.root, folder)
	} else {
		pf := s.ensureFolderLocked(proj, parent)
		pf.Children = append(pf.Children, folder)
	}
	return folder
}

// Uploads returns a copy of all recorded upload operations.
func (s *Server) Uploads() []UploadRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]UploadRecord, len(s.uploads))
	copy(result, s.uploads)
	return result
}

// VersionsRequests returns how many GET /v2/files/{id}/versions/ requests the
// server has served. Lets a test assert that the scan skips the version-history
// fetch when the directory listing already settles a file's classification.
func (s *Server) VersionsRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versionReq
}

// ListRequests returns how many file-listing *pages* the server has served.
// Because the fake paginates like real OSF (default 10 per page, capped at
// 100), this is what proves datapin asks for a large page size instead of walking
// a folder ten items at a time (issue #86).
func (s *Server) ListRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listReq
}

// Deletes returns the file IDs (from /v1/files/{id}/delete) that received a DELETE.
func (s *Server) Deletes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, len(s.deletes))
	copy(result, s.deletes)
	return result
}

// Moves returns a copy of all recorded move/copy/rename actions.
func (s *Server) Moves() []MoveRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]MoveRecord, len(s.moves))
	copy(result, s.moves)
	return result
}

// NodePatches returns a copy of all recorded PATCH /nodes/{id}/ requests.
func (s *Server) NodePatches() []NodePatchRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]NodePatchRecord, len(s.patches))
	copy(result, s.patches)
	return result
}

// Folders returns a copy of all folder paths created via kind=folder PUT.
func (s *Server) Folders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, len(s.folders))
	copy(result, s.folders)
	return result
}

// GetProject returns the fakeProject for id, or nil.
func (s *Server) GetProject(id string) (title, description, category string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	proj := s.projects[id]
	if proj == nil {
		return "", "", ""
	}
	return proj.title, proj.description, proj.category
}

// GetFile returns the File at filePath in projectID, or nil.
func (s *Server) GetFile(projectID, filePath string) *File {
	s.mu.Lock()
	defer s.mu.Unlock()
	proj := s.projects[projectID]
	if proj == nil {
		return nil
	}
	return proj.byPath[filePath]
}

// --- HTTP routing ---

func (s *Server) handler(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && p == "/v2/users/me/":
		s.handleCurrentUser(w, r)
	case r.Method == http.MethodGet && p == "/v2/users/me/nodes/":
		s.handleUserNodes(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/nodes/") && strings.Contains(p, "/files/osfstorage/"):
		s.handleFileList(w, r)
	case strings.HasPrefix(p, "/v2/nodes/") && strings.HasSuffix(p, "/wikis/"):
		s.handleNodeWikis(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/nodes/") && strings.HasSuffix(p, "/contributors/"):
		s.handleContributors(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/nodes/") && strings.HasSuffix(p, "/children/"):
		s.handleChildren(w, r)
	case strings.HasPrefix(p, "/v2/wikis/"):
		s.handleWiki(w, r)
	case (r.Method == http.MethodGet || r.Method == http.MethodPatch) && strings.HasPrefix(p, "/v2/nodes/"):
		if r.Method == http.MethodPatch {
			s.handleNodePatch(w, r)
		} else {
			s.handleNode(w, r)
		}
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/files/") && strings.HasSuffix(p, "/versions/"):
		s.handleVersions(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v1/files/") && !strings.HasSuffix(p, "/upload") && !strings.HasSuffix(p, "/delete") && !strings.HasSuffix(p, "/move"):
		s.handleDownload(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/v1/files/") && strings.HasSuffix(p, "/move"):
		s.handleMoveAction(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/v1/resources/"):
		s.handleNewUpload(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/v1/files/") && strings.HasSuffix(p, "/upload"):
		s.handleVersionUpload(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/v1/files/"):
		s.handleDelete(w, r)
	default:
		http.NotFound(w, r)
	}
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Header already sent; nothing useful we can do.
		_ = err
	}
}

func (s *Server) fileItemJSON(nodeID string, f *File) map[string]any {
	base := s.URL()
	kind := "file"
	if f.IsFolder {
		kind = "folder"
	}
	size := int64(0)
	md5Str := ""
	currentVersion := 0
	if !f.IsFolder && len(f.Versions) > 0 {
		latest := f.Versions[len(f.Versions)-1]
		size = int64(len(latest.content))
		md5Str = latest.md5
		currentVersion = latest.num
	}
	folderHref := ""
	if f.IsFolder {
		folderHref = fmt.Sprintf("%s/v2/nodes/%s/files/osfstorage/?parent=%s", base, nodeID, f.ID)
	}
	return map[string]any{
		"id": f.ID,
		"attributes": map[string]any{
			"name":              f.Name,
			"kind":              kind,
			"size":              size,
			"date_modified":     "2024-01-01T00:00:00",
			"date_created":      "2024-01-01T00:00:00",
			"materialized_path": f.FilePath,
			"current_version":   currentVersion,
			"extra":             map[string]any{"hashes": map[string]any{"md5": md5Str}},
		},
		"links": s.itemLinks(nodeID, f),
		"relationships": map[string]any{
			"files": map[string]any{
				"links": map[string]any{
					"related": map[string]any{
						"href": folderHref,
					},
				},
			},
		},
	}
}

// itemLinks builds the action links for a file or folder. A folder's upload /
// new_folder links are ID-based Waterbutler resource URLs (osfstorage addresses
// folders by opaque ID); a file's upload link is its own version-upload endpoint.
func (s *Server) itemLinks(nodeID string, f *File) map[string]any {
	base := s.URL()
	links := map[string]any{
		"download": fmt.Sprintf("%s/v1/files/%s", base, f.ID),
		"delete":   fmt.Sprintf("%s/v1/files/%s/delete", base, f.ID),
		"move":     fmt.Sprintf("%s/v1/files/%s/move", base, f.ID),
	}
	if f.IsFolder {
		folderBase := fmt.Sprintf("%s/v1/resources/%s/providers/osfstorage/%s/", base, nodeID, f.ID)
		links["upload"] = folderBase
		links["new_folder"] = folderBase + "?kind=folder"
	} else {
		links["upload"] = fmt.Sprintf("%s/v1/files/%s/upload", base, f.ID)
	}
	return links
}

// --- Handler implementations ---

// handleCurrentUser serves GET /v2/users/me/, validating the bearer token.
func (s *Server) handleCurrentUser(w http.ResponseWriter, r *http.Request) {
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	valid := s.validToken
	s.mu.Unlock()

	authOK := bearer != "" && (valid == "" || bearer == valid)
	if !authOK {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"errors": []map[string]any{{"detail": "User provided an invalid OAuth2 access token"}},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"id": "me01d",
			"attributes": map[string]any{
				"full_name":     "Test User",
				"given_name":    "Test",
				"family_name":   "User",
				"email_primary": "test@example.com",
				"active":        true,
			},
		},
	})
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	nodeID := extractNodeID(r.URL.Path)
	if s.forbid(w, nodeID) {
		return
	}
	s.mu.Lock()
	proj, ok := s.projects[nodeID]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": nodeJSON(proj),
	})
}

// nodeJSON renders a project as a JSON:API node resource.
func nodeJSON(proj *fakeProject) map[string]any {
	tags := proj.tags
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{
		"id": proj.id,
		"attributes": map[string]any{
			"title":         proj.title,
			"description":   proj.description,
			"date_created":  "2024-01-01T00:00:00",
			"date_modified": "2024-01-01T00:00:00",
			"public":        true,
			"category":      proj.category,
			"tags":          tags,
		},
	}
}

// handleContributors serves GET /v2/nodes/{id}/contributors/ with the user
// resource embedded, as real OSF does.
func (s *Server) handleContributors(w http.ResponseWriter, r *http.Request) {
	nodeID := extractNodeID(r.URL.Path)
	if s.forbid(w, nodeID) {
		return
	}
	s.mu.Lock()
	proj, ok := s.projects[nodeID]
	if !ok {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	data := make([]map[string]any, 0, len(proj.contributors))
	for i, c := range proj.contributors {
		data = append(data, map[string]any{
			"id":   fmt.Sprintf("%s-user%d", nodeID, i+1),
			"type": "contributors",
			"attributes": map[string]any{
				"bibliographic": c.Bibliographic,
				"index":         i,
			},
			"embeds": map[string]any{
				"users": map[string]any{
					"data": map[string]any{
						"id": fmt.Sprintf("user%d", i+1),
						"attributes": map[string]any{
							"full_name":   c.FullName,
							"given_name":  c.GivenName,
							"family_name": c.FamilyName,
						},
					},
				},
			},
		})
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  data,
		"links": map[string]any{"next": nil},
	})
}

// handleChildren serves GET /v2/nodes/{id}/children/ (direct components).
func (s *Server) handleChildren(w http.ResponseWriter, r *http.Request) {
	nodeID := extractNodeID(r.URL.Path)
	if s.forbid(w, nodeID) {
		return
	}
	s.mu.Lock()
	proj, ok := s.projects[nodeID]
	if !ok {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	data := make([]map[string]any, 0, len(proj.children))
	for _, childID := range proj.children {
		if child := s.projects[childID]; child != nil {
			data = append(data, nodeJSON(child))
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  data,
		"links": map[string]any{"next": nil},
	})
}

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	nodeID := extractNodeID(r.URL.Path)
	parentID := r.URL.Query().Get("parent")

	if s.forbid(w, nodeID) {
		return
	}
	s.mu.Lock()
	proj, ok := s.projects[nodeID]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	var items []*File
	if parentID == "" {
		items = proj.root
	} else {
		folder, exists := proj.byID[parentID]
		if !exists || !folder.IsFolder {
			http.NotFound(w, r)
			return
		}
		items = folder.Children
	}

	s.mu.Lock()
	s.listReq++
	s.mu.Unlock()

	page, size := pageParams(r)
	window, nextPage := paginate(len(items), page, size)

	data := make([]map[string]any, 0, len(window))
	for _, i := range window {
		data = append(data, s.fileItemJSON(nodeID, items[i]))
	}
	var next any
	if nextPage > 0 {
		u := *r.URL
		q := u.Query()
		q.Set("page", strconv.Itoa(nextPage))
		q.Set("page[size]", strconv.Itoa(size))
		u.RawQuery = q.Encode()
		next = s.URL() + u.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  data,
		"links": map[string]any{"next": next, "meta": map[string]any{"total": len(items), "per_page": size}},
	})
}

// defaultPageSize and maxServedPageSize mirror real OSF: an unqualified request
// gets 10 items, and page[size] is honoured up to 100 and silently capped
// beyond it. datapin's fake used to return everything in one page with next=nil,
// so no test ever exercised the multi-page walk.
const (
	defaultPageSize   = 10
	maxServedPageSize = 100
)

// pageParams reads the JSON:API page cursor and size from a request.
func pageParams(r *http.Request) (page, size int) {
	page = 1
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		page = v
	}
	size = defaultPageSize
	if v, err := strconv.Atoi(r.URL.Query().Get("page[size]")); err == nil && v > 0 {
		size = v
		if size > maxServedPageSize {
			size = maxServedPageSize
		}
	}
	return page, size
}

// paginate returns the indices belonging to the requested page and the number
// of the next page (0 when this is the last).
func paginate(total, page, size int) (indices []int, nextPage int) {
	start := (page - 1) * size
	if start >= total {
		return nil, 0
	}
	end := start + size
	if end > total {
		end = total
	}
	for i := start; i < end; i++ {
		indices = append(indices, i)
	}
	if end < total {
		nextPage = page + 1
	}
	return indices, nextPage
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	// /v2/files/{fileID}/versions/
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	fileID := parts[2]

	s.mu.Lock()
	s.versionReq++
	f, ok := s.allFiles[fileID]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Return versions newest-first (matching OSF API behaviour).
	versions := make([]map[string]any, 0, len(f.Versions))
	for i := len(f.Versions) - 1; i >= 0; i-- {
		v := f.Versions[i]
		versions = append(versions, map[string]any{
			// Real OSF carries the version number as the JSON:API id (e.g. "2"),
			// with NO version attribute — model that faithfully.
			"id": fmt.Sprintf("%d", v.num),
			"attributes": map[string]any{
				"size":         int64(len(v.content)),
				"date_created": "2024-01-01T00:00:00",
				"extra": map[string]any{
					"hashes": map[string]any{"md5": v.md5},
				},
			},
			"embeds": map[string]any{
				"user": map[string]any{
					"data": map[string]any{
						"id": "user1",
						"attributes": map[string]any{
							"full_name":     "Test User",
							"email_primary": "test@example.com",
						},
					},
				},
			},
		})
	}
	page, size := pageParams(r)
	window, nextPage := paginate(len(versions), page, size)
	pageData := make([]map[string]any, 0, len(window))
	for _, i := range window {
		pageData = append(pageData, versions[i])
	}
	var next any
	if nextPage > 0 {
		u := *r.URL
		q := u.Query()
		q.Set("page", strconv.Itoa(nextPage))
		q.Set("page[size]", strconv.Itoa(size))
		u.RawQuery = q.Encode()
		next = s.URL() + u.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  pageData,
		"links": map[string]any{"next": next},
	})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	fileID := strings.TrimPrefix(r.URL.Path, "/v1/files/")

	s.mu.Lock()
	f, ok := s.allFiles[fileID]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	rev := 0
	if revStr := r.URL.Query().Get("revision"); revStr != "" {
		fmt.Sscanf(revStr, "%d", &rev) //nolint:errcheck
	}

	var content []byte
	if rev > 0 {
		content = f.versionContent(rev)
	} else {
		content = f.LatestContent()
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) handleNewUpload(w http.ResponseWriter, r *http.Request) {
	// /v1/resources/{nodeID}/providers/osfstorage/{parentPath}?name={name}&kind=file|folder
	rest := strings.TrimPrefix(r.URL.Path, "/v1/resources/")
	const provSuffix = "/providers/osfstorage/"
	slashIdx := strings.Index(rest, provSuffix)
	if slashIdx < 0 {
		http.Error(w, "invalid upload URL", http.StatusBadRequest)
		return
	}
	nodeID := rest[:slashIdx]
	// The segment after osfstorage/ is the parent folder's opaque object ID
	// (empty = storage root) — NOT a name. This mirrors real Waterbutler, which
	// 404s a name-based path; it is what lets these tests catch the subfolder
	// upload bug that a name-lenient fake would hide.
	parentID := strings.Trim(rest[slashIdx+len(provSuffix):], "/")

	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing name param", http.StatusBadRequest)
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "file"
	}

	if kind == "folder" {
		// Like file upload, the destination is addressed by the parent folder's
		// opaque ID (empty = root); a name/unknown segment 404s, as real OSF does.
		s.mu.Lock()
		proj, ok := s.projects[nodeID]
		if !ok {
			s.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		parentPath := "/"
		if parentID != "" {
			pf, found := proj.byID[parentID]
			if !found || !pf.IsFolder {
				s.mu.Unlock()
				http.NotFound(w, r)
				return
			}
			parentPath = pf.FilePath
		}
		fullPath := strings.TrimRight(parentPath, "/") + "/" + name
		s.ensureFolderLocked(proj, fullPath)
		s.folders = append(s.folders, fullPath)
		s.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]any{
			"data": map[string]any{
				"attributes": map[string]any{"name": name, "kind": "folder"},
			},
		})
		return
	}

	content, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h := md5.Sum(content)
	md5Str := fmt.Sprintf("%x", h[:])

	s.mu.Lock()
	proj, ok := s.projects[nodeID]
	if !ok {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	var parent *File
	if parentID != "" {
		pf, found := proj.byID[parentID]
		if !found || !pf.IsFolder {
			// Unknown/non-folder id (e.g. a name-built path) → 404, like real OSF.
			s.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		parent = pf
	}
	fullPath := "/" + name
	if parent != nil {
		fullPath = parent.FilePath + "/" + name
	}
	// Real OSF rejects a create (kind=file) when a file of that name already
	// exists in the folder with 409 Conflict — the caller must PUT a new version
	// to the file's own upload link instead. Model that faithfully (#62).
	if _, exists := proj.byPath[fullPath]; exists {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{
			"errors": []any{map[string]any{"detail": "Conflict"}},
		})
		return
	}
	s.nextID++
	f := &File{
		ID:       fmt.Sprintf("f%d", s.nextID),
		Name:     name,
		FilePath: fullPath,
		Versions: []fileVer{{num: 1, content: content, md5: md5Str}},
	}
	proj.byID[f.ID] = f
	proj.byPath[fullPath] = f
	s.allFiles[f.ID] = f
	if parent == nil {
		proj.root = append(proj.root, f)
	} else {
		parent.Children = append(parent.Children, f)
	}
	s.uploads = append(s.uploads, UploadRecord{NodeID: nodeID, Path: fullPath, Content: content})
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{
			"attributes": map[string]any{
				"version": 1,
				"size":    int64(len(content)),
				"extra":   map[string]any{"hashes": map[string]any{"md5": md5Str}},
			},
		},
	})
}

func (s *Server) handleVersionUpload(w http.ResponseWriter, r *http.Request) {
	// /v1/files/{fileID}/upload
	fileID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/files/"), "/upload")

	content, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h := md5.Sum(content)
	md5Str := fmt.Sprintf("%x", h[:])

	s.mu.Lock()
	f, ok := s.allFiles[fileID]
	newVer := 1
	var fullPath string
	if ok {
		newVer = f.LatestVersion() + 1
		f.Versions = append(f.Versions, fileVer{num: newVer, content: content, md5: md5Str})
		fullPath = f.FilePath
	}
	s.uploads = append(s.uploads, UploadRecord{NodeID: "", Path: fullPath, Content: content})
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"attributes": map[string]any{
				"version": newVer,
				"size":    int64(len(content)),
				"extra":   map[string]any{"hashes": map[string]any{"md5": md5Str}},
			},
		},
	})
}

func (s *Server) handleUserNodes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	nodes := make([]map[string]any, 0, len(s.projects))
	for _, proj := range s.projects {
		nodes = append(nodes, map[string]any{
			"id": proj.id,
			"attributes": map[string]any{
				"title":         proj.title,
				"description":   "",
				"date_created":  "2024-01-01T00:00:00",
				"date_modified": "2024-01-01T00:00:00",
				"public":        true,
				"category":      "project",
			},
		})
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  nodes,
		"links": map[string]any{"next": nil},
	})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	// /v1/files/{fileID}/delete
	fileID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/files/"), "/delete")
	s.mu.Lock()
	s.deletes = append(s.deletes, fileID)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMoveAction(w http.ResponseWriter, r *http.Request) {
	// POST /v1/files/{fileID}/move
	fileID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/files/"), "/move")

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	action, _ := body["action"].(string)
	destPath, _ := body["path"].(string)
	newName, _ := body["rename"].(string)
	resource, _ := body["resource"].(string)
	conflict, _ := body["conflict"].(string)

	rec := MoveRecord{
		FileID:   fileID,
		Action:   action,
		DestPath: destPath,
		NewName:  newName,
		Resource: resource,
		Conflict: conflict,
	}

	s.mu.Lock()
	s.moves = append(s.moves, rec)
	f := s.allFiles[fileID]
	s.mu.Unlock()

	if f == nil {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"attributes": map[string]any{
				"name": func() string {
					if newName != "" {
						return newName
					}
					return f.Name
				}(),
				"kind": "file",
			},
		},
	})
}

func (s *Server) handleNodePatch(w http.ResponseWriter, r *http.Request) {
	// PATCH /v2/nodes/{nodeID}/
	nodeID := extractNodeID(r.URL.Path)

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, _ := body["data"].(map[string]any)
	attrs, _ := data["attributes"].(map[string]any)

	s.mu.Lock()
	proj, ok := s.projects[nodeID]
	if !ok {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if v, ok := attrs["title"].(string); ok {
		proj.title = v
	}
	if v, ok := attrs["description"].(string); ok {
		proj.description = v
	}
	if v, ok := attrs["category"].(string); ok {
		proj.category = v
	}
	s.patches = append(s.patches, NodePatchRecord{NodeID: nodeID, Attrs: attrs})
	title, desc, cat := proj.title, proj.description, proj.category
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"id": nodeID,
			"attributes": map[string]any{
				"title":         title,
				"description":   desc,
				"date_created":  "2024-01-01T00:00:00",
				"date_modified": "2024-01-01T00:00:00",
				"public":        true,
				"category":      cat,
			},
		},
	})
}

// --- URL parsing helpers ---

// extractNodeID pulls the node GUID from a /v2/nodes/{nodeID}/... path.
func extractNodeID(p string) string {
	rest := strings.TrimPrefix(p, "/v2/nodes/")
	if idx := strings.Index(rest, "/"); idx >= 0 {
		return rest[:idx]
	}
	return rest
}
