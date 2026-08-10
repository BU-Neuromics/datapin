package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/BU-Neuromics/datapin/internal/env"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const metaBase = "https://api.osf.io/v2"

// OSFClient is an HTTP client for the OSF JSON:API metadata endpoints.
type OSFClient struct {
	token   string
	http    *http.Client
	baseURL string
	// sleep waits out a rate-limit delay. Injectable so tests exercise the
	// retry logic without real time passing; it must honour ctx so Ctrl-C
	// during a throttle wait still aborts the run.
	sleep func(ctx context.Context, d time.Duration) error
}

// New returns an OSFClient. Pass an empty token for unauthenticated (public) access.
// Set DATAPIN_API_BASE (or legacy GOSF_API_BASE) to override the default API base URL (useful in tests).
func New(token string) *OSFClient {
	base := env.Get("API_BASE")
	if base == "" {
		base = metaBase
	}
	return &OSFClient{
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		baseURL: base,
		sleep:   sleepContext,
	}
}

// --- domain types ---

// Node represents a project or component.
type Node struct {
	ID         string         `json:"id"`
	Attributes NodeAttributes `json:"attributes"`
}

// NodeAttributes holds the metadata fields we care about for a node.
type NodeAttributes struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	DateCreated  string `json:"date_created"`
	DateModified string `json:"date_modified"`
	Public       bool   `json:"public"`
	Category     string `json:"category"`
}

// FileItem represents a single file or folder entry in OSF Storage.
type FileItem struct {
	ID            string            `json:"id"`
	Attributes    FileAttributes    `json:"attributes"`
	Links         FileLinks         `json:"links"`
	Relationships FileRelationships `json:"relationships"`
}

// FileAttributes holds metadata about a file or folder.
type FileAttributes struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"` // "file" or "folder"
	Size             int64  `json:"size"`
	DateModified     string `json:"date_modified"`
	DateCreated      string `json:"date_created"`
	MaterializedPath string `json:"materialized_path"`
	ContentType      string `json:"content_type"`
	// CurrentVersion is the latest version number OSF reports for a file in a
	// directory listing (attributes.current_version). It lets a scan learn the
	// remote's latest version without a separate /files/{id}/versions/ call.
	// 0 for folders or when the field is absent.
	CurrentVersion int `json:"current_version"`
	// Extra carries the content hashes OSF reports for files under
	// attributes.extra.hashes. Folders report null hashes, which decode to
	// empty strings; omitempty keeps them out of the JSON output.
	Extra FileExtra `json:"extra"`
}

// FileExtra holds the OSF "extra" attribute block for a file.
type FileExtra struct {
	Hashes FileHashes `json:"hashes"`
}

// FileHashes holds the content hashes OSF computes for a file version.
type FileHashes struct {
	MD5    string `json:"md5,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// FileLinks contains action URLs for a file.
type FileLinks struct {
	Download string `json:"download"`
	Upload   string `json:"upload"`
	Delete   string `json:"delete"`
	Self     string `json:"self"`
	Move     string `json:"move"`
}

// FileRelationships holds links to related resources (e.g. folder contents).
type FileRelationships struct {
	Files struct {
		Links struct {
			Related struct {
				Href string `json:"href"`
			} `json:"related"`
		} `json:"links"`
	} `json:"files"`
}

// User represents an OSF user.
type User struct {
	ID         string         `json:"id"`
	Attributes UserAttributes `json:"attributes"`
}

// UserAttributes holds user profile fields.
type UserAttributes struct {
	FullName     string `json:"full_name"`
	GivenName    string `json:"given_name"`
	FamilyName   string `json:"family_name"`
	EmailPrimary string `json:"email_primary"`
	Active       bool   `json:"active"`
}

// APIError represents a non-2xx response from the OSF API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("OSF API %d: %s", e.StatusCode, e.Message)
}

// maxPageSize is the largest page OSF's JSON:API will serve. The default is 10,
// so a listing that does not ask for a size costs up to 10× the requests it
// needs — the dominant source of rate-limit pressure on a large manifest
// (issue #86). Asking for more than 100 is silently capped at 100.
const maxPageSize = 100

// withPageSize returns rawURL with page[size]=size added, leaving an existing
// page[size] untouched: OSF's links.next already carries the size forward, and
// a duplicate key would leave the server to choose between them. A URL that
// cannot be parsed is returned unchanged — the request will fail on its own
// terms rather than on a mangled query string.
func withPageSize(rawURL string, size int) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	if q.Has("page[size]") {
		return rawURL
	}
	q.Set("page[size]", strconv.Itoa(size))
	u.RawQuery = q.Encode()
	return u.String()
}

// --- internal HTTP helpers ---

// doGet issues an authenticated GET, retrying throttled and transient-gateway
// responses with the wait OSF asks for (see retry.go). The retry loop is here
// rather than in an http.RoundTripper so it can read the parsed status and log
// a user-visible reason for the pause.
func (c *OSFClient) doGet(ctx context.Context, url string) ([]byte, int, error) {
	var body []byte
	var status int

	for attempt := 0; ; attempt++ {
		var retryAfter string
		var err error
		body, status, retryAfter, err = c.getOnce(ctx, url)
		if err != nil {
			return nil, 0, err
		}
		d, ok := parseRetryAfter(retryAfter, time.Now())
		delay := retryDelay(attempt, d, ok)
		if !shouldRetry(status, attempt, delay) {
			return body, status, nil
		}
		logRetry(status, attempt, delay)
		if err := c.sleep(ctx, delay); err != nil {
			return nil, 0, err
		}
	}
}

// getOnce performs a single GET and returns the body, status, and Retry-After
// header (empty when absent).
func (c *OSFClient) getOnce(ctx context.Context, url string) ([]byte, int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, "", err
	}
	req.Header.Set("Accept", "application/vnd.api+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, resp.Header.Get("Retry-After"), err
}

func (c *OSFClient) getJSON(ctx context.Context, url string, v any) error {
	body, status, err := c.doGet(ctx, url)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return parseAPIError(status, body)
	}
	return json.Unmarshal(body, v)
}

func parseAPIError(statusCode int, body []byte) *APIError {
	var errResp struct {
		Errors []struct {
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	msg := http.StatusText(statusCode)
	if json.Unmarshal(body, &errResp) == nil && len(errResp.Errors) > 0 && errResp.Errors[0].Detail != "" {
		msg = errResp.Errors[0].Detail
	}
	return &APIError{StatusCode: statusCode, Message: msg}
}

// --- public API ---

// GetFileItem returns metadata for a single file by its OSF file ID.
func (c *OSFClient) GetFileItem(ctx context.Context, fileID string) (*FileItem, error) {
	var result struct {
		Data FileItem `json:"data"`
	}
	url := fmt.Sprintf("%s/files/%s/", c.baseURL, fileID)
	if err := c.getJSON(ctx, url, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// GetUserNodes returns all nodes (projects and components) the authenticated
// user has access to. Requires auth.
func (c *OSFClient) GetUserNodes(ctx context.Context) ([]Node, error) {
	return c.listNodesFromURL(ctx, c.baseURL+"/users/me/nodes/")
}

type nodesPage struct {
	Data  []Node `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

func (c *OSFClient) listNodesFromURL(ctx context.Context, url string) ([]Node, error) {
	var all []Node
	for url != "" {
		var page nodesPage
		if err := c.getJSON(ctx, withPageSize(url, maxPageSize), &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		url = page.Links.Next
	}
	return all, nil
}

// GetCurrentUser returns the authenticated user's profile.
// Returns APIError with StatusCode 401 if the token is invalid.
func (c *OSFClient) GetCurrentUser(ctx context.Context) (*User, error) {
	var result struct {
		Data User `json:"data"`
	}
	if err := c.getJSON(ctx, c.baseURL+"/users/me/", &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// GetNode returns metadata for the given project/component GUID.
func (c *OSFClient) GetNode(ctx context.Context, nodeID string) (*Node, error) {
	var result struct {
		Data Node `json:"data"`
	}
	url := fmt.Sprintf("%s/nodes/%s/", c.baseURL, nodeID)
	if err := c.getJSON(ctx, url, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// ListFiles returns all items (files and folders) at the root of a node's OSF Storage.
// Pages are followed automatically.
func (c *OSFClient) ListFiles(ctx context.Context, nodeID string) ([]FileItem, error) {
	url := fmt.Sprintf("%s/nodes/%s/files/osfstorage/", c.baseURL, nodeID)
	return c.listFilesFromURL(ctx, url)
}

// ListFilesFromURL lists files at an arbitrary OSF files endpoint URL, following pagination.
func (c *OSFClient) ListFilesFromURL(ctx context.Context, url string) ([]FileItem, error) {
	return c.listFilesFromURL(ctx, url)
}

type filesPage struct {
	Data  []FileItem `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

func (c *OSFClient) listFilesFromURL(ctx context.Context, url string) ([]FileItem, error) {
	var all []FileItem
	for url != "" {
		var page filesPage
		if err := c.getJSON(ctx, withPageSize(url, maxPageSize), &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		url = page.Links.Next
	}
	return all, nil
}

// FileVersion represents one version of a file.
type FileVersion struct {
	ID         string                `json:"id"`
	Attributes FileVersionAttributes `json:"attributes"`
	Embeds     struct {
		User struct {
			Data User `json:"data"`
		} `json:"user"`
	} `json:"embeds"`
}

// FileVersionAttributes holds per-version metadata.
type FileVersionAttributes struct {
	Version     int       `json:"version"`
	Size        int64     `json:"size"`
	DateCreated string    `json:"date_created"`
	ContentType string    `json:"content_type"`
	Extra       FileExtra `json:"extra"`
}

// Number returns the version number. The OSF versions endpoint carries it as the
// JSON:API resource id (e.g. "2"), not as an attribute, so we parse the id and
// fall back to attributes.version only when the id is non-numeric.
func (v FileVersion) Number() int {
	if n, err := strconv.Atoi(v.ID); err == nil {
		return n
	}
	return v.Attributes.Version
}

// Contributor returns the best available identifier for the user who created this version:
// email_primary > full_name > GUID.
func (v FileVersion) Contributor() string {
	u := v.Embeds.User.Data
	if u.Attributes.EmailPrimary != "" {
		return u.Attributes.EmailPrimary
	}
	if u.Attributes.FullName != "" {
		return u.Attributes.FullName
	}
	return u.ID
}

// UpdateNodeAttrs holds the fields that may be updated via PATCH /nodes/{id}/.
// A nil pointer means "do not change this field".
type UpdateNodeAttrs struct {
	Title       *string
	Description *string
	Category    *string
	Tags        []string // nil = don't update; non-nil replaces the full list
}

// UpdateNode patches writable metadata fields on an OSF project or component.
// Only non-nil fields in attrs are included in the PATCH body.
func (c *OSFClient) UpdateNode(ctx context.Context, nodeID string, attrs UpdateNodeAttrs) (*Node, error) {
	attributes := map[string]any{}
	if attrs.Title != nil {
		attributes["title"] = *attrs.Title
	}
	if attrs.Description != nil {
		attributes["description"] = *attrs.Description
	}
	if attrs.Category != nil {
		attributes["category"] = *attrs.Category
	}
	if attrs.Tags != nil {
		attributes["tags"] = attrs.Tags
	}
	if len(attributes) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	payload := map[string]any{
		"data": map[string]any{
			"id":         nodeID,
			"type":       "nodes",
			"attributes": attributes,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/nodes/%s/", c.baseURL, nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/vnd.api+json")
	req.Header.Set("Accept", "application/vnd.api+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var result struct {
		Data Node `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// GetFileVersions returns all versions of a file, newest-first.
//
// Note: the OSF v2 versions endpoint does not expose a user relationship and
// rejects ?embed=user with a 400 ("The following fields are not embeddable:
// user"), so no embed is requested. Contributor info is therefore unavailable
// from this endpoint unless the response happens to carry embedded user data.
func (c *OSFClient) GetFileVersions(ctx context.Context, fileID string) ([]FileVersion, error) {
	url := fmt.Sprintf("%s/files/%s/versions/", c.baseURL, fileID)
	return c.listVersionsFromURL(ctx, url)
}

type versionsPage struct {
	Data  []FileVersion `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

func (c *OSFClient) listVersionsFromURL(ctx context.Context, url string) ([]FileVersion, error) {
	var all []FileVersion
	for url != "" {
		var page versionsPage
		if err := c.getJSON(ctx, withPageSize(url, maxPageSize), &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		url = page.Links.Next
	}
	return all, nil
}
