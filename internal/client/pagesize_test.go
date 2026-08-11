package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

// OSF's JSON:API defaults to 10 items per page and caps at 100. datapin never
// asked for a size, so every listing took the default and a folder of 87 files
// cost 9 requests instead of 1 — the dominant source of rate-limit pressure
// (issue #86).
func TestWithPageSize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"adds the parameter when absent",
			"https://api.osf.io/v2/nodes/abc12/files/osfstorage/",
			"https://api.osf.io/v2/nodes/abc12/files/osfstorage/?page%5Bsize%5D=100",
		},
		{
			"preserves existing query parameters",
			"https://api.osf.io/v2/nodes/abc12/files/osfstorage/?filter%5Bkind%5D=file",
			"https://api.osf.io/v2/nodes/abc12/files/osfstorage/?filter%5Bkind%5D=file&page%5Bsize%5D=100",
		},
		{
			// links.next already carries the size forward; re-adding it would
			// duplicate the key and risk the server picking the wrong one.
			"leaves an existing page[size] alone",
			"https://api.osf.io/v2/nodes/abc12/files/osfstorage/?page%5Bsize%5D=25",
			"https://api.osf.io/v2/nodes/abc12/files/osfstorage/?page%5Bsize%5D=25",
		},
		{
			"keeps the page cursor when adding a size",
			"https://api.osf.io/v2/nodes/abc12/files/osfstorage/?page=3",
			"https://api.osf.io/v2/nodes/abc12/files/osfstorage/?page=3&page%5Bsize%5D=100",
		},
		{
			"passes through a URL it cannot parse rather than corrupting it",
			"://not a url",
			"://not a url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := withPageSize(tt.in, 100); got != tt.want {
				t.Errorf("withPageSize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// countingServer records the page[size] seen on each request.
func countingServer(t *testing.T, body func(page int) string) (*httptest.Server, *int64, *[]string) {
	t.Helper()
	var n int64
	sizes := make([]string, 0, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		sizes = append(sizes, r.URL.Query().Get("page[size]"))
		w.Header().Set("Content-Type", "application/vnd.api+json")
		page := 1
		if p := r.URL.Query().Get("page"); p == "2" {
			page = 2
		}
		_, _ = w.Write([]byte(body(page)))
	}))
	t.Cleanup(srv.Close)
	return srv, &n, &sizes
}

// Every paginated endpoint must ask for the maximum page size.
func TestListEndpointsRequestMaxPageSize(t *testing.T) {
	empty := func(int) string { return `{"data":[],"links":{"next":null}}` }

	cases := []struct {
		name string
		call func(c *OSFClient) error
	}{
		{"ListFiles", func(c *OSFClient) error { _, err := c.ListFiles(context.Background(), "abc12"); return err }},
		{"GetUserNodes", func(c *OSFClient) error { _, err := c.GetUserNodes(context.Background()); return err }},
		{"GetFileVersions", func(c *OSFClient) error { _, err := c.GetFileVersions(context.Background(), "f1"); return err }},
		{"ListWikis", func(c *OSFClient) error { _, err := c.ListWikis(context.Background(), "abc12"); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, sizes := countingServer(t, empty)
			c := New("tok")
			c.baseURL = srv.URL
			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if len(*sizes) == 0 {
				t.Fatal("no request was made")
			}
			if (*sizes)[0] != "100" {
				t.Errorf("%s requested page[size]=%q, want 100", tc.name, (*sizes)[0])
			}
		})
	}
}

// A folder listing that spans pages must be followed to the end, and the
// follow-up requests must not re-add a page[size] the next link already has.
func TestListFilesFollowsPagesWithoutDuplicatingPageSize(t *testing.T) {
	var next string
	srv, count, sizes := countingServer(t, func(page int) string {
		if page == 1 {
			return `{"data":[{"id":"f1","attributes":{"name":"a.csv","kind":"file"}}],"links":{"next":"` + next + `"}}`
		}
		return `{"data":[{"id":"f2","attributes":{"name":"b.csv","kind":"file"}}],"links":{"next":null}}`
	})
	// The server's next link carries the size forward, as OSF's does.
	next = srv.URL + "/nodes/abc12/files/osfstorage/?page=2&page%5Bsize%5D=100"

	c := New("tok")
	c.baseURL = srv.URL
	items, err := c.ListFiles(context.Background(), "abc12")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("got %d items across pages, want 2", len(items))
	}
	if *count != 2 {
		t.Errorf("made %d requests, want 2 (one per page)", *count)
	}
	for i, s := range *sizes {
		if s != "100" {
			t.Errorf("request %d used page[size]=%q, want 100", i+1, s)
		}
	}
	// The second URL must carry exactly one page[size] key.
	u, _ := url.Parse(next)
	if got := len(u.Query()["page[size]"]); got != 1 {
		t.Errorf("next URL carries %d page[size] keys, want 1", got)
	}
}
