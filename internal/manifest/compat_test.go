package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// ---- legacy .gosf/gosf.toml compatibility (read-only migration path) ----

func chdir(t *testing.T, dir string) {
	t.Helper()
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
}

func TestFindManifest_LegacyGosfFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".gosf"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".gosf"), "gosf.toml", validTOML)
	chdir(t, dir)

	manifestPath, repoRoot, err := manifest.FindManifest()
	if err != nil {
		t.Fatalf("FindManifest with only a legacy manifest: %v", err)
	}
	if manifestPath != filepath.Join(dir, ".gosf", "gosf.toml") {
		t.Errorf("manifestPath = %q, want the legacy .gosf/gosf.toml", manifestPath)
	}
	if repoRoot != dir {
		t.Errorf("repoRoot = %q, want %q", repoRoot, dir)
	}
}

func TestFindManifest_PrefersNewOverLegacy(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{".datapin", ".gosf"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, ".datapin"), "datapin.toml", validTOML)
	writeFile(t, filepath.Join(dir, ".gosf"), "gosf.toml", validTOML)
	chdir(t, dir)

	manifestPath, _, err := manifest.FindManifest()
	if err != nil {
		t.Fatalf("FindManifest: %v", err)
	}
	if manifestPath != filepath.Join(dir, ".datapin", "datapin.toml") {
		t.Errorf("manifestPath = %q, want the new .datapin/datapin.toml to win", manifestPath)
	}
}

func TestFindManifest_LegacyInParentDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gosf"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".gosf"), "gosf.toml", validTOML)
	subdir := filepath.Join(root, "deep", "nested")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	chdir(t, subdir)

	_, repoRoot, err := manifest.FindManifest()
	if err != nil {
		t.Fatalf("FindManifest: %v", err)
	}
	if repoRoot != root {
		t.Errorf("repoRoot = %q, want %q", repoRoot, root)
	}
}

// A new-format manifest in a subdirectory must win over a legacy one further
// up: the walk stops at the first directory carrying either manifest.
func TestFindManifest_NewInChildWinsOverLegacyInParent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gosf"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".gosf"), "gosf.toml", validTOML)
	child := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(child, ".datapin"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(child, ".datapin"), "datapin.toml", validTOML)
	chdir(t, child)

	manifestPath, repoRoot, err := manifest.FindManifest()
	if err != nil {
		t.Fatalf("FindManifest: %v", err)
	}
	if manifestPath != filepath.Join(child, ".datapin", "datapin.toml") {
		t.Errorf("manifestPath = %q, want the child's .datapin/datapin.toml", manifestPath)
	}
	if repoRoot != child {
		t.Errorf("repoRoot = %q, want %q", repoRoot, child)
	}
}

func TestSave_RefusesLegacyManifestPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".gosf"), 0755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, ".gosf", "gosf.toml")
	writeFile(t, filepath.Join(dir, ".gosf"), "gosf.toml", validTOML)

	m, err := manifest.Load(legacyPath)
	if err != nil {
		t.Fatalf("Load legacy manifest: %v", err)
	}

	err = manifest.Save(m, legacyPath)
	if err == nil {
		t.Fatal("Save to a legacy .gosf/gosf.toml must be refused (legacy manifests are read-only)")
	}
	if !strings.Contains(err.Error(), ".datapin/datapin.toml") {
		t.Errorf("the error must tell the user how to migrate, got: %v", err)
	}

	// The refusal must leave the legacy file untouched.
	data, readErr := os.ReadFile(legacyPath)
	if readErr != nil {
		t.Fatalf("reading legacy manifest after refused Save: %v", readErr)
	}
	if string(data) != validTOML {
		t.Error("refused Save must not modify the legacy manifest")
	}
}

func TestSave_AcceptsNewManifestPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".datapin"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".datapin", "datapin.toml")
	writeFile(t, filepath.Join(dir, ".datapin"), "datapin.toml", validTOML)

	m, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := manifest.Save(m, path); err != nil {
		t.Fatalf("Save to the new manifest path must succeed: %v", err)
	}
}
