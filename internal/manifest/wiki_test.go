package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "datapin.toml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadWikiEntries(t *testing.T) {
	p := writeManifest(t, `
[project]
id = "abc12"

[[files]]
local = "data/x.csv"
remote = "/data/x.csv"

[[wikis]]
local = "docs/home.md"
page = "home"
version = 3
md5 = "d41d8cd98f00b204e9800998ecf8427e"

[[wikis]]
local = "docs/notes.md"
page = "Analysis Notes"
project = "xyz89"
`)
	m, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Wikis) != 2 {
		t.Fatalf("got %d wiki entries, want 2", len(m.Wikis))
	}
	w := m.Wikis[0]
	if w.Local != "docs/home.md" || w.Page != "home" || w.Version != 3 {
		t.Errorf("first wiki entry = %+v", w)
	}
	if got := m.Wikis[1].ResolveProject(m.Project.ID); got != "xyz89" {
		t.Errorf("ResolveProject = %q, want xyz89", got)
	}
	if got := m.Wikis[0].ResolveProject(m.Project.ID); got != "abc12" {
		t.Errorf("ResolveProject = %q, want abc12", got)
	}
}

func TestWikiEntrySaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "datapin.toml")
	m := &Manifest{
		Project: ProjectConfig{ID: "abc12"},
		Wikis: []WikiEntry{
			{Local: "docs/home.md", Page: "home", Version: 2, MD5: "aa"},
		},
	}
	if err := Save(m, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Wikis) != 1 || got.Wikis[0] != m.Wikis[0] {
		t.Errorf("round trip = %+v", got.Wikis)
	}
}

func TestWikiValidation(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			"no project",
			`[[wikis]]
local = "a.md"
page = "a"`,
			"no project",
		},
		{
			"duplicate local across wikis",
			`[project]
id = "abc12"
[[wikis]]
local = "a.md"
page = "a"
[[wikis]]
local = "a.md"
page = "b"`,
			"duplicate local path",
		},
		{
			"duplicate local across files and wikis",
			`[project]
id = "abc12"
[[files]]
local = "a.md"
remote = "/a.md"
[[wikis]]
local = "a.md"
page = "a"`,
			"duplicate local path",
		},
		{
			"duplicate project+page",
			`[project]
id = "abc12"
[[wikis]]
local = "a.md"
page = "home"
[[wikis]]
local = "b.md"
page = "home"`,
			"duplicate (project, page)",
		},
		{
			"blank page name",
			`[project]
id = "abc12"
[[wikis]]
local = "a.md"
page = ""`,
			"page name",
		},
		{
			"slash in page name",
			`[project]
id = "abc12"
[[wikis]]
local = "a.md"
page = "a/b"`,
			"forward slashes",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeManifest(t, c.toml)
			_, err := Load(p)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func TestWikiPageNameTooLong(t *testing.T) {
	long := strings.Repeat("x", 101)
	p := writeManifest(t, `
[project]
id = "abc12"
[[wikis]]
local = "a.md"
page = "`+long+`"
`)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "100 characters") {
		t.Errorf("error = %v", err)
	}
}

// TestClassifyWikiEntry proves a wiki entry classifies through the same state
// machine as files by converting its baseline into the Entry shape.
func TestClassifyWikiEntry(t *testing.T) {
	we := WikiEntry{Local: "docs/home.md", Page: "home", Version: 2, MD5: "bb"}
	remote := []RemoteVersion{{Version: 3, MD5: "cc"}, {Version: 2, MD5: "bb"}, {Version: 1, MD5: "aa"}}

	// L == B, R newer → REMOTE_NEWER
	if got := ClassifyFile(we.BaselineEntry(), "bb", remote, false); got != StateRemoteNewer {
		t.Errorf("got %v, want REMOTE_NEWER", got)
	}
	// L matches latest → PIN_ONLY (pin stale)
	if got := ClassifyFile(we.BaselineEntry(), "cc", remote, false); got != StatePinOnly {
		t.Errorf("got %v, want PIN_ONLY", got)
	}
	// L unique, R newer → DIVERGED
	if got := ClassifyFile(we.BaselineEntry(), "zz", remote, false); got != StateDivergent {
		t.Errorf("got %v, want DIVERGED", got)
	}
}
