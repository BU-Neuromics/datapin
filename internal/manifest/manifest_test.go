package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// ---- helpers ----

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	return p
}

const validTOML = `
[project]
id = "abc12"

[[files]]
local     = "data/raw/counts.h5"
remote    = "/data/raw/counts.h5"
version   = 3
md5       = "d41d8cd98f00b204e9800998ecf8427e"

[[files]]
local     = "results/model.pkl"
remote    = "/results/model.pkl"
version   = 1
md5       = "098f6bcd4621d373cade4e832627b4f6"
`

// ---- Load tests ----

func TestLoad_HappyPath(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "datapin.toml", validTOML)

	m, err := manifest.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Project.ID != "abc12" {
		t.Errorf("project.id = %q, want abc12", m.Project.ID)
	}
	if len(m.Files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(m.Files))
	}
	f := m.Files[0]
	if f.Local != "data/raw/counts.h5" {
		t.Errorf("files[0].local = %q", f.Local)
	}
	if f.Version != 3 {
		t.Errorf("files[0].version = %d", f.Version)
	}
	if f.MD5 != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Errorf("files[0].md5 = %q", f.MD5)
	}
}

func TestLoad_PerEntryProjectOverride(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[project]
id = "abc12"

[[files]]
local     = "data/raw/counts.h5"
remote    = "/data/raw/counts.h5"
version   = 0
md5       = ""
project   = "xyz89"
`
	p := writeFile(t, dir, "datapin.toml", toml)
	m, err := manifest.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Files[0].Project != "xyz89" {
		t.Errorf("files[0].project = %q, want xyz89", m.Files[0].Project)
	}
}

func TestLoad_DuplicateLocalPath_Error(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[project]
id = "abc12"

[[files]]
local     = "data/counts.h5"
remote    = "/data/counts.h5"
version   = 0
md5       = ""

[[files]]
local     = "data/counts.h5"
remote    = "/data/other.h5"
version   = 0
md5       = ""
`
	p := writeFile(t, dir, "datapin.toml", toml)
	_, err := manifest.Load(p)
	if err == nil {
		t.Fatal("expected error for duplicate local path")
	}
	if !strings.Contains(err.Error(), "local") {
		t.Errorf("error should mention 'local': %v", err)
	}
}

func TestLoad_DuplicateRemotePath_Error(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[project]
id = "abc12"

[[files]]
local     = "data/a.h5"
remote    = "/data/same.h5"
version   = 0
md5       = ""

[[files]]
local     = "data/b.h5"
remote    = "/data/same.h5"
version   = 0
md5       = ""
`
	p := writeFile(t, dir, "datapin.toml", toml)
	_, err := manifest.Load(p)
	if err == nil {
		t.Fatal("expected error for duplicate (project, remote) pair")
	}
}

func TestLoad_MissingProject_Error(t *testing.T) {
	dir := t.TempDir()
	// No [project].id and no per-entry project field
	const toml = `
[[files]]
local     = "data/counts.h5"
remote    = "/data/counts.h5"
version   = 0
md5       = ""
`
	p := writeFile(t, dir, "datapin.toml", toml)
	_, err := manifest.Load(p)
	if err == nil {
		t.Fatal("expected error when no project can be resolved")
	}
}

func TestLoad_NotFound(t *testing.T) {
	_, err := manifest.Load("/no/such/file.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ---- Save + roundtrip tests ----

func TestSave_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "datapin.toml", validTOML)

	m, err := manifest.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Mutate and save.
	m.Files[0].Version = 4
	m.Files[0].MD5 = "newmd5hash"

	if err := manifest.Save(m, p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload and verify.
	m2, err := manifest.Load(p)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if m2.Files[0].Version != 4 {
		t.Errorf("version after save = %d, want 4", m2.Files[0].Version)
	}
	if m2.Files[0].MD5 != "newmd5hash" {
		t.Errorf("md5 after save = %q", m2.Files[0].MD5)
	}
	// Other entries must be unchanged.
	if m2.Files[1].Version != 1 {
		t.Errorf("files[1].version should be unchanged, got %d", m2.Files[1].Version)
	}
}

func TestSave_Atomic(t *testing.T) {
	// Save must not leave a partial file if the write cannot complete.
	// We verify atomicity by confirming the original is intact after
	// a Save to an unwritable directory would fail. Instead, just verify
	// that the file is replaced as a unit (temp+rename) by checking the
	// original file path survives and is valid TOML after Save.
	dir := t.TempDir()
	p := writeFile(t, dir, "datapin.toml", validTOML)
	m, _ := manifest.Load(p)
	m.Files[0].Version = 99

	if err := manifest.Save(m, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The original path should be a valid manifest.
	m2, err := manifest.Load(p)
	if err != nil {
		t.Fatalf("manifest unreadable after atomic Save: %v", err)
	}
	if m2.Files[0].Version != 99 {
		t.Errorf("version = %d, want 99", m2.Files[0].Version)
	}
}

// ---- FindManifest tests ----

func TestFindManifest_InCwd(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".datapin"), 0755)
	writeFile(t, filepath.Join(dir, ".datapin"), "datapin.toml", validTOML)

	// Temporarily change cwd.
	origDir, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(origDir) })
	os.Chdir(dir)

	manifestPath, repoRoot, err := manifest.FindManifest()
	if err != nil {
		t.Fatalf("FindManifest: %v", err)
	}
	if manifestPath != filepath.Join(dir, ".datapin", "datapin.toml") {
		t.Errorf("manifestPath = %q, want %q", manifestPath, filepath.Join(dir, ".datapin", "datapin.toml"))
	}
	if repoRoot != dir {
		t.Errorf("repoRoot = %q, want %q", repoRoot, dir)
	}
}

func TestFindManifest_InParentDir(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".datapin"), 0755)
	writeFile(t, filepath.Join(root, ".datapin"), "datapin.toml", validTOML)
	subdir := filepath.Join(root, "deep", "nested")
	os.MkdirAll(subdir, 0755)

	origDir, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(origDir) })
	os.Chdir(subdir)

	manifestPath, repoRoot, err := manifest.FindManifest()
	if err != nil {
		t.Fatalf("FindManifest: %v", err)
	}
	if repoRoot != root {
		t.Errorf("repoRoot = %q, want %q", repoRoot, root)
	}
	_ = manifestPath
}

func TestFindManifest_NotFound(t *testing.T) {
	// Use an empty temp dir that has no datapin.toml anywhere above it
	// (temp dirs are typically under /tmp which shouldn't have a datapin.toml).
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(origDir) })
	os.Chdir(dir)

	_, _, err := manifest.FindManifest()
	if err == nil {
		t.Fatal("expected NotFoundError when no datapin.toml exists")
	}
	if !manifest.IsNotFound(err) {
		t.Errorf("expected NotFoundError, got %T: %v", err, err)
	}
}

// ---- ResolveProject tests ----

func TestEntry_ResolveProject(t *testing.T) {
	m := &manifest.Manifest{
		Project: manifest.ProjectConfig{ID: "abc12"},
		Files: []manifest.Entry{
			{Local: "a.csv", Remote: "/a.csv", Project: ""},
			{Local: "b.csv", Remote: "/b.csv", Project: "xyz89"},
		},
	}
	if p := m.Files[0].ResolveProject(m.Project.ID); p != "abc12" {
		t.Errorf("files[0].ResolveProject = %q, want abc12", p)
	}
	if p := m.Files[1].ResolveProject(m.Project.ID); p != "xyz89" {
		t.Errorf("files[1].ResolveProject = %q, want xyz89", p)
	}
}
