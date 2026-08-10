package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

func TestInit_CreatesManifest(t *testing.T) {
	dir := t.TempDir()
	path, created, err := manifest.Init(dir, "abc12")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !created {
		t.Error("created should be true for new manifest")
	}
	want := filepath.Join(dir, ".datapin", "datapin.toml")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load after Init: %v", err)
	}
	if m.Project.ID != "abc12" {
		t.Errorf("project.id = %q, want abc12", m.Project.ID)
	}
	if len(m.Files) != 0 {
		t.Errorf("files should be empty, got %d", len(m.Files))
	}
}

func TestInit_CreatesDatapinDir(t *testing.T) {
	dir := t.TempDir()
	_, _, err := manifest.Init(dir, "abc12")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".datapin")); os.IsNotExist(statErr) {
		t.Error("Init should create .datapin/ directory")
	}
}

func TestInit_UpdatesExistingManifest(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".datapin"), 0755)
	writeFile(t, filepath.Join(dir, ".datapin"), "datapin.toml", validTOML)

	path, created, err := manifest.Init(dir, "xyz99")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if created {
		t.Error("created should be false for existing manifest")
	}
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load after Init: %v", err)
	}
	if m.Project.ID != "xyz99" {
		t.Errorf("project.id = %q, want xyz99", m.Project.ID)
	}
	if len(m.Files) != 2 {
		t.Errorf("files count = %d, want 2 (existing entries preserved)", len(m.Files))
	}
}

func TestInit_PreservesFilesWithNoProject(t *testing.T) {
	// Init should not fail even when the existing manifest has no project.id set
	// (the file entries have per-entry project fields keeping them valid).
	dir := t.TempDir()
	const toml = `
[[files]]
local     = "data/counts.h5"
remote    = "/data/counts.h5"
version   = 0
md5       = ""
project   = "xyz89"
`
	os.MkdirAll(filepath.Join(dir, ".datapin"), 0755)
	writeFile(t, filepath.Join(dir, ".datapin"), "datapin.toml", toml)

	_, _, err := manifest.Init(dir, "abc12")
	if err != nil {
		t.Fatalf("Init on manifest with no default project: %v", err)
	}
}

func TestInit_ReturnedPathIsInsideDatapinDir(t *testing.T) {
	dir := t.TempDir()
	path, _, err := manifest.Init(dir, "abc12")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	want := filepath.Join(dir, ".datapin", "datapin.toml")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}
