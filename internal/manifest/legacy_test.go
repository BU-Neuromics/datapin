package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// A manifest written by datapin ≤1.9 carries a `direction` key on every entry.
// The field is retired (issue #81), but old manifests must keep working: the
// key is accepted and ignored, never a load error.
func TestLoad_LegacyDirectionKeyIsAcceptedAndIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "datapin.toml")
	content := `[project]
id = "abc12"

[[files]]
local     = "data/in.csv"
remote    = "/data/in.csv"
direction = "pull"
version   = 3
md5       = "aaa"

[[files]]
local     = "out.csv"
remote    = "/out.csv"
direction = "sideways"
version   = 0

[[wikis]]
local     = "docs/home.md"
page      = "home"
direction = "both"
version   = 2
md5       = "bb"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("legacy manifest must load, got: %v", err)
	}
	if len(m.Files) != 2 || len(m.Wikis) != 1 {
		t.Fatalf("got %d files / %d wikis, want 2/1", len(m.Files), len(m.Wikis))
	}
	if m.Files[0].Version != 3 || m.Files[0].MD5 != "aaa" {
		t.Errorf("files[0] lost its pin: %+v", m.Files[0])
	}
	if m.Wikis[0].Page != "home" || m.Wikis[0].Version != 2 {
		t.Errorf("wikis[0] lost its fields: %+v", m.Wikis[0])
	}
}

// A manifest with no `direction` anywhere is now the normal case and must load.
func TestLoad_NoDirectionIsValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "datapin.toml")
	content := `[project]
id = "abc12"

[[files]]
local   = "x.csv"
remote  = "/x.csv"
version = 0

[[wikis]]
local   = "docs/home.md"
page    = "home"
version = 0
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Load(path); err != nil {
		t.Fatalf("a manifest without direction must load, got: %v", err)
	}
}

// Re-saving a legacy manifest drops the retired key while preserving every
// other field, including [[wikis]] (guards #80).
func TestSave_DropsLegacyDirectionAndKeepsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "datapin.toml")
	content := `[project]
id = "abc12"

[[files]]
local     = "data/in.csv"
remote    = "/data/in.csv"
direction = "pull"
version   = 3
md5       = "aaa"
project   = "xyz89"

[[wikis]]
local     = "docs/home.md"
page      = "home"
direction = "both"
version   = 2
md5       = "bb"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Save(m, path); err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "direction") {
		t.Errorf("saved manifest still carries direction:\n%s", got)
	}
	for _, want := range []string{
		`local = 'data/in.csv'`, `remote = '/data/in.csv'`, `md5 = 'aaa'`,
		`project = 'xyz89'`, `[[wikis]]`, `page = 'home'`, `md5 = 'bb'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("saved manifest lost %q:\n%s", want, got)
		}
	}

	// The round trip must reload cleanly.
	m2, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if len(m2.Files) != 1 || len(m2.Wikis) != 1 || m2.Files[0].Project != "xyz89" {
		t.Errorf("round trip lost data: %+v", m2)
	}
}

// LegacyDirectionCount is the pure detector behind the one-time load warning.
func TestLegacyDirectionCount(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want int
	}{
		{"none", "[project]\nid = \"abc12\"\n", 0},
		{"one file", "[[files]]\nlocal = \"a\"\ndirection = \"push\"\n", 1},
		{
			"files and wikis",
			"[[files]]\nlocal = \"a\"\ndirection = \"push\"\n\n[[files]]\nlocal = \"b\"\n\n[[wikis]]\nlocal = \"c\"\ndirection = \"pull\"\n",
			2,
		},
		{"unparseable", "not toml at all {{", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := manifest.LegacyDirectionCount([]byte(tt.toml)); got != tt.want {
				t.Errorf("LegacyDirectionCount = %d, want %d", got, tt.want)
			}
		})
	}
}
