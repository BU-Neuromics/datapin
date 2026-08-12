package site_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/site"
)

func TestWriteDirAndDeployGHPages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	out, err := site.Build(buildInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dir := t.TempDir()
	if err := site.WriteDir(out, dir); err != nil {
		t.Fatalf("WriteDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "datasets", "counts", "index.html")); err != nil {
		t.Fatalf("written tree incomplete: %v", err)
	}

	// A local bare repo stands in for GitHub.
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %s", out)
	}

	if err := site.DeployGHPages(context.Background(), dir, bare, ""); err != nil {
		t.Fatalf("DeployGHPages: %v", err)
	}

	// gh-pages exists, carries the site, and has exactly one commit.
	lsTree, err := exec.Command("git", "-C", bare, "ls-tree", "-r", "--name-only", "gh-pages").Output()
	if err != nil {
		t.Fatalf("ls-tree: %v", err)
	}
	tree := string(lsTree)
	for _, want := range []string{"index.html", "style.css", ".nojekyll", "datasets/counts/index.html"} {
		if !strings.Contains(tree, want) {
			t.Errorf("gh-pages missing %s:\n%s", want, tree)
		}
	}
	count, err := exec.Command("git", "-C", bare, "rev-list", "--count", "gh-pages").Output()
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if strings.TrimSpace(string(count)) != "1" {
		t.Errorf("gh-pages has %s commits, want exactly 1 (orphan)", strings.TrimSpace(string(count)))
	}

	// Redeploy still yields a single commit (force-pushed orphan, no history).
	if err := site.DeployGHPages(context.Background(), dir, bare, ""); err != nil {
		t.Fatalf("second DeployGHPages: %v", err)
	}
	count, _ = exec.Command("git", "-C", bare, "rev-list", "--count", "gh-pages").Output()
	if strings.TrimSpace(string(count)) != "1" {
		t.Errorf("after redeploy gh-pages has %s commits, want 1", strings.TrimSpace(string(count)))
	}
}

func TestEnablePages(t *testing.T) {
	var method, path, auth string
	status := 201
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(status)
	}))
	defer srv.Close()

	st, err := site.EnablePages(context.Background(), srv.URL, "org/repo", "tok")
	if err != nil {
		t.Fatalf("EnablePages: %v", err)
	}
	if method != "POST" || path != "/repos/org/repo/pages" || auth != "Bearer tok" {
		t.Errorf("request = %s %s auth=%q", method, path, auth)
	}
	if !st.Created || !st.ServingSite() {
		t.Errorf("a fresh 201 must report Created and a gh-pages source: %+v", st)
	}

	status = 409 // already enabled — not an error
	if _, err := site.EnablePages(context.Background(), srv.URL, "org/repo", "tok"); err != nil {
		t.Errorf("409 must be treated as already-enabled: %v", err)
	}

	status = 404
	if _, err := site.EnablePages(context.Background(), srv.URL, "org/repo", "tok"); err == nil {
		t.Error("404 must surface as an error")
	}
}

// A repo whose Pages is already switched on, but pointed somewhere other
// than gh-pages, is the trap: the deploy succeeds, the POST 409s, and the
// site silently keeps serving the old source. EnablePages must read the
// existing config back and report it so the caller can say so out loud.
func TestEnablePages_AlreadyEnabledOnAnotherSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"html_url":"https://org.github.io/repo/",
			"source":{"branch":"main","path":"/"}}`))
	}))
	defer srv.Close()

	st, err := site.EnablePages(context.Background(), srv.URL, "org/repo", "tok")
	if err != nil {
		t.Fatalf("EnablePages: %v", err)
	}
	if st.Created {
		t.Error("Created must be false when Pages was already configured")
	}
	if st.Branch != "main" || st.Path != "/" {
		t.Errorf("existing source not reported: %+v", st)
	}
	if st.ServingSite() {
		t.Error("a main/ source is not serving what DeployGHPages pushed")
	}
	if st.URL != "https://org.github.io/repo/" {
		t.Errorf("URL = %q, want the html_url from the API", st.URL)
	}
}

// The read-back is best-effort: a 409 whose follow-up GET fails must still
// not fail the publish — the bytes are already on gh-pages.
func TestEnablePages_ConflictWithUnreadableConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	st, err := site.EnablePages(context.Background(), srv.URL, "org/repo", "tok")
	if err != nil {
		t.Fatalf("a 409 with an unreadable config must not fail the publish: %v", err)
	}
	if st.Created || st.Branch != "" {
		t.Errorf("unknown source should stay unknown: %+v", st)
	}
	if st.ServingSite() {
		t.Error("an unknown source must not claim to be serving the site")
	}
}
