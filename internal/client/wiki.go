package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Wiki represents one wiki page on a node.
type Wiki struct {
	ID         string         `json:"id"`
	Attributes WikiAttributes `json:"attributes"`
	Links      WikiLinks      `json:"links"`
}

// WikiAttributes holds metadata about a wiki page.
type WikiAttributes struct {
	Name             string    `json:"name"`
	Kind             string    `json:"kind"` // always "file"
	Size             int64     `json:"size"`
	DateModified     string    `json:"date_modified"`
	ContentType      string    `json:"content_type"` // always "text/markdown"
	Path             string    `json:"path"`         // "/{wiki_id}"
	MaterializedPath string    `json:"materialized_path"`
	Extra            WikiExtra `json:"extra"`
}

// WikiExtra carries the current version number of a wiki page.
type WikiExtra struct {
	Version int `json:"version"`
}

// WikiLinks contains URLs for a wiki page.
type WikiLinks struct {
	Download string `json:"download"`
	Info     string `json:"info"`
	Self     string `json:"self"`
}

// WikiVersion represents one version of a wiki page. The version number is the
// JSON:API resource id ("1", "2", …).
type WikiVersion struct {
	ID         string                `json:"id"`
	Attributes WikiVersionAttributes `json:"attributes"`
	Embeds     struct {
		User struct {
			Data User `json:"data"`
		} `json:"user"`
	} `json:"embeds"`
}

// WikiVersionAttributes holds per-version metadata. Wiki versions carry no
// content hash — callers hash fetched content themselves.
type WikiVersionAttributes struct {
	DateCreated string `json:"date_created"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// Number returns the integer version number parsed from the resource id, or 0
// when the id is not numeric.
func (v WikiVersion) Number() int {
	if n, err := strconv.Atoi(v.ID); err == nil {
		return n
	}
	return 0
}

// Contributor returns the best available identifier for the user who created
// this version: email_primary > full_name > GUID. Usually empty — the wikis
// versions endpoint rarely embeds user data.
func (v WikiVersion) Contributor() string {
	u := v.Embeds.User.Data
	if u.Attributes.EmailPrimary != "" {
		return u.Attributes.EmailPrimary
	}
	if u.Attributes.FullName != "" {
		return u.Attributes.FullName
	}
	return u.ID
}

// CanonicalizeWikiContent returns the canonical form of wiki content used for
// hashing and equality comparison. OSF does NOT store wiki content byte-for-byte:
// it normalizes line endings to LF and trims surrounding whitespace (its DRF
// content field is trim_whitespace=True, and CRLF is converted to LF on write).
// datapin therefore cannot promise a byte-exact round trip; instead it compares a
// canonical form so idempotent pushes and sync classification are stable. The
// function is idempotent and is applied to BOTH local and remote content, so
// agreement holds regardless of OSF's exact internal rule.
func CanonicalizeWikiContent(b []byte) []byte {
	s := string(b)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return []byte(strings.TrimSpace(s))
}

// IsWikiDisabled reports whether err is the OSF 404 that signals the wiki
// addon is disabled for the node (as opposed to a missing node or page). The
// match is on a substring of OSF's "The wiki for this node has been disabled."
// detail, tolerating punctuation/casing drift.
func IsWikiDisabled(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.StatusCode == http.StatusNotFound &&
		strings.Contains(apiErr.Message, "wiki for this node has been disabled")
}

// ListWikis returns all wiki pages of a node, following pagination. OSF orders
// them by most recently modified first.
func (c *OSFClient) ListWikis(ctx context.Context, nodeID string) ([]Wiki, error) {
	url := fmt.Sprintf("%s/nodes/%s/wikis/", c.baseURL, nodeID)
	var all []Wiki
	for url != "" {
		var page struct {
			Data  []Wiki `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		if err := c.getJSON(ctx, withPageSize(url, maxPageSize), &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		url = page.Links.Next
	}
	return all, nil
}

// GetWikiContent returns the raw markdown content of a wiki page's latest
// version, byte-exact (the endpoint serves plain text, not JSON).
func (c *OSFClient) GetWikiContent(ctx context.Context, wikiID string) ([]byte, error) {
	url := fmt.Sprintf("%s/wikis/%s/content/", c.baseURL, wikiID)
	return c.getText(ctx, url)
}

// GetWikiVersions returns all versions of a wiki page, following pagination.
// OSF returns them newest-first.
func (c *OSFClient) GetWikiVersions(ctx context.Context, wikiID string) ([]WikiVersion, error) {
	url := fmt.Sprintf("%s/wikis/%s/versions/", c.baseURL, wikiID)
	var all []WikiVersion
	for url != "" {
		var page struct {
			Data  []WikiVersion `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		if err := c.getJSON(ctx, withPageSize(url, maxPageSize), &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		url = page.Links.Next
	}
	return all, nil
}

// GetWikiVersionContent returns the raw markdown content of one historical
// version of a wiki page.
func (c *OSFClient) GetWikiVersionContent(ctx context.Context, wikiID, versionID string) ([]byte, error) {
	url := fmt.Sprintf("%s/wikis/%s/versions/%s/content/", c.baseURL, wikiID, versionID)
	return c.getText(ctx, url)
}

// CreateWiki creates a new wiki page on a node with the given name and initial
// content. The server rejects duplicate names with a 409.
func (c *OSFClient) CreateWiki(ctx context.Context, nodeID, name, content string) (*Wiki, error) {
	payload := map[string]any{
		"data": map[string]any{
			"type": "wikis",
			"attributes": map[string]any{
				"name":    name,
				"content": content,
			},
		},
	}
	url := fmt.Sprintf("%s/nodes/%s/wikis/", c.baseURL, nodeID)
	var result struct {
		Data Wiki `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// CreateWikiVersion updates a wiki page by creating a new version with the
// given content. Every call mints a new immutable version (current + 1).
func (c *OSFClient) CreateWikiVersion(ctx context.Context, wikiID, content string) (*WikiVersion, error) {
	payload := map[string]any{
		"data": map[string]any{
			"type": "wiki-versions",
			"attributes": map[string]any{
				"content": content,
			},
		},
	}
	url := fmt.Sprintf("%s/wikis/%s/versions/", c.baseURL, wikiID)
	var result struct {
		Data WikiVersion `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// RenameWiki renames a wiki page. The server refuses to rename the home page
// and rejects a name that already exists (409).
func (c *OSFClient) RenameWiki(ctx context.Context, wikiID, newName string) (*Wiki, error) {
	payload := map[string]any{
		"data": map[string]any{
			"id":   wikiID,
			"type": "wikis",
			"attributes": map[string]any{
				"name": newName,
			},
		},
	}
	url := fmt.Sprintf("%s/wikis/%s/", c.baseURL, wikiID)
	var result struct {
		Data Wiki `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPatch, url, payload, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// DeleteWiki deletes a wiki page. The server refuses to delete the home page.
func (c *OSFClient) DeleteWiki(ctx context.Context, wikiID string) error {
	url := fmt.Sprintf("%s/wikis/%s/", c.baseURL, wikiID)
	return c.doJSON(ctx, http.MethodDelete, url, nil, nil)
}

// getText GETs a plain-text endpoint and returns the raw body bytes.
//
// The wiki content endpoints (/wikis/{id}/content/ and the per-version variant)
// are served by OSF's PlainTextRenderer, whose media type is text/markdown. They
// do NOT speak application/vnd.api+json — sending that Accept header (as the
// JSON metadata calls do) yields a 406 Not Acceptable. So this request advertises
// text/markdown explicitly, with a */* fallback.
func (c *OSFClient) getText(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/markdown, */*")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseAPIError(resp.StatusCode, body)
	}
	return body, nil
}

// doJSON sends a JSON:API request with the given method and optional payload,
// decoding the response into result when result is non-nil.
func (c *OSFClient) doJSON(ctx context.Context, method, url string, payload, result any) error {
	var reqBody io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/vnd.api+json")
	}
	req.Header.Set("Accept", "application/vnd.api+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, body)
	}
	if result != nil {
		return json.Unmarshal(body, result)
	}
	return nil
}
