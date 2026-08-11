//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupSite extends the dataset fixture with site pages.
func (e *invenioEnv) setupSite(t *testing.T) {
	t.Helper()
	e.setupDataset(t)
	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(e.dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("docs/index.md", "---\ntitle: Home\n---\n\n# Project home\n\nHello.")
	mustWrite("docs/methods.md", "# Methods\n\nDetails.")
	p := filepath.Join(e.dir, ".datapin", "datapin.toml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	site := `
[site]
title    = "Integration Site"
base_url = "https://example.org/site"

[[site.pages]]
local = "docs/index.md"
[[site.pages]]
local = "docs/methods.md"
`
	if err := os.WriteFile(p, append(data, []byte(site)...), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSiteBuild(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupSite(t)
	mustPublish(t, e, "counts")

	stdout, stderr, code := e.run("site", "build")
	if code != 0 {
		t.Fatalf("site build: code=%d\n%s%s", code, stdout, stderr)
	}

	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(e.dir, "public", rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		return string(data)
	}
	index := read("index.html")
	if !strings.Contains(index, "Project home") || !strings.Contains(index, "Integration Site") {
		t.Errorf("index.html incomplete:\n%s", index)
	}
	landing := read("datasets/counts/index.html")
	if !strings.Contains(landing, "10.5072/zenodo.") || !strings.Contains(landing, "counts.csv") {
		t.Errorf("landing page missing DOI or files:\n%s", landing)
	}
	if !strings.Contains(landing, `"@type": "Dataset"`) {
		t.Error("landing page missing schema.org JSON-LD")
	}
	if !strings.Contains(read("sitemap.xml"), "https://example.org/site/methods/") {
		t.Error("sitemap missing methods page")
	}
	read(".nojekyll")
}

func TestSitePublish_ToLocalRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	e := newInvenioEnv(t)
	e.setupSite(t)

	// A local bare repo as origin; no [site].repo configured, so publish
	// resolves the enclosing repo's origin.
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %s", out)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", bare},
	} {
		cmd := exec.Command("git", append([]string{"-C", e.dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}

	stdout, stderr, code := e.run("site", "publish", "--output=json")
	if code != 0 {
		t.Fatalf("site publish: code=%d\n%s%s", code, stdout, stderr)
	}
	lsTree, err := exec.Command("git", "-C", bare, "ls-tree", "-r", "--name-only", "gh-pages").Output()
	if err != nil {
		t.Fatalf("ls-tree: %v", err)
	}
	if !strings.Contains(string(lsTree), "datasets/counts/index.html") {
		t.Errorf("gh-pages missing site content:\n%s", lsTree)
	}
}
