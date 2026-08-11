//go:build integration

package integration

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// migrateResult mirrors output.MigrateResult for JSON assertions.
type migrateResult struct {
	Mode       string `json:"mode"`
	Source     string `json:"source"`
	Manifest   string `json:"manifest"`
	Downloaded []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	} `json:"downloaded"`
	Skipped []struct {
		Path string `json:"path"`
	} `json:"skipped"`
	WikiPages []struct {
		Page  string `json:"page"`
		Local string `json:"local"`
	} `json:"wiki_pages"`
	Datasets []struct {
		Slug  string `json:"slug"`
		Files int    `json:"files"`
	} `json:"datasets"`
	ComponentsSkipped []string `json:"components_skipped"`
	TODOs             []string `json:"todos"`
	DryRun            bool     `json:"dry_run"`
}

func md5hex(s string) string { return fmt.Sprintf("%x", md5.Sum([]byte(s))) }

// setupMigrateProject registers a public-ish project with files, wikis,
// tags, and a contributor — the GUID-mode fixture.
func setupMigrateProject(e *testEnv) {
	e.srv.AddProject("abc12", "Neuro Project")
	e.srv.SetProjectMeta("abc12", "A study of things", "rna-seq", "mouse")
	e.srv.AddContributor("abc12", "Ada Lovelace", "Ada", "Lovelace")
	e.srv.AddFile("abc12", "/data/raw/counts.h5", []byte("counts"))
	e.srv.AddFile("abc12", "/data/design.csv", []byte("design"))
	e.srv.AddFile("abc12", "/README.md", []byte("readme"))
	e.srv.AddWiki("abc12", "home", []byte("# Home\n"))
	e.srv.AddWiki("abc12", "Lab Notes", []byte("notes\n"))
}

func TestMigrate_GUIDMode(t *testing.T) {
	e := newTestEnv(t)
	setupMigrateProject(e)

	_, stderr, code := e.run("migrate", "abc12")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}

	// Files downloaded, structure preserved.
	if got := e.readFile("data/raw/counts.h5"); got != "counts" {
		t.Errorf("counts.h5 = %q", got)
	}
	if got := e.readFile("README.md"); got != "readme" {
		t.Errorf("README.md = %q", got)
	}

	// Wiki pages exported to docs/.
	if got := e.readFile("docs/home.md"); !strings.Contains(got, "# Home") {
		t.Errorf("docs/home.md = %q", got)
	}
	if !e.fileExists("docs/lab-notes.md") {
		t.Error("docs/lab-notes.md not exported")
	}

	// Manifest scaffolded: one dataset, metadata skeleton with TODO markers,
	// provenance related identifier, site pages for the wikis.
	manifest := e.readFile(".datapin/datapin.toml")
	for _, want := range []string{
		"[[datasets]]",
		"neuro-project",        // dataset slug from the node title
		"Neuro Project",        // metadata title
		"A study of things",    // description
		"rna-seq",              // keywords from tags
		"Lovelace, Ada",        // contributor → creator skeleton
		"TODO",                 // license / contact_email markers
		"IsDerivedFrom",        // provenance relation
		"https://osf.io/abc12", // provenance identifier
		"data/raw/counts.h5",   // file keys preserve storage paths
		"docs/home.md",         // site page
		"docs/lab-notes.md",    // site page
		"resource_type = 'dataset'",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "[[files]]") || strings.Contains(manifest, "[[wikis]]") {
		t.Errorf("a migrated manifest must not reference OSF sections:\n%s", manifest)
	}

	// Provenance breadcrumb.
	migrated := e.readFile("MIGRATED.md")
	for _, want := range []string{"https://osf.io/abc12", "license", "datapin publish"} {
		if !strings.Contains(migrated, want) {
			t.Errorf("MIGRATED.md missing %q:\n%s", want, migrated)
		}
	}
}

func TestMigrate_GUIDMode_JSONAndIdempotentRerun(t *testing.T) {
	e := newTestEnv(t)
	setupMigrateProject(e)

	stdout, stderr, code := e.run("migrate", "abc12", "--output=json")
	// TODO markers remain (license, ORCIDs, contact) — JSON mode signals
	// incompleteness with exit 1, like `datapin status`.
	if code != 1 {
		t.Fatalf("exit %d, want 1 (todos remain); stderr=%s", code, stderr)
	}
	var r migrateResult
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if r.Mode != "guid" || r.Source != "abc12" || r.DryRun {
		t.Errorf("header = %+v", r)
	}
	if len(r.Downloaded) != 3 || len(r.Skipped) != 0 {
		t.Errorf("downloaded=%d skipped=%d, want 3/0", len(r.Downloaded), len(r.Skipped))
	}
	if len(r.WikiPages) != 2 {
		t.Errorf("wiki_pages = %+v", r.WikiPages)
	}
	if len(r.Datasets) != 1 || r.Datasets[0].Files != 3 {
		t.Errorf("datasets = %+v", r.Datasets)
	}
	hasLicense := false
	for _, todo := range r.TODOs {
		if todo == "license" {
			hasLicense = true
		}
	}
	if !hasLicense {
		t.Errorf("todos = %v, want license", r.TODOs)
	}

	// The user fills in the license, then re-runs: nothing re-downloads and
	// the edit survives.
	manifest := e.readFile(".datapin/datapin.toml")
	var out []string
	for _, line := range strings.Split(manifest, "\n") {
		if strings.Contains(line, "license") && strings.Contains(line, "TODO") {
			line = "license = 'CC0-1.0'"
		}
		out = append(out, line)
	}
	e.writeFile(".datapin/datapin.toml", strings.Join(out, "\n"))

	stdout, stderr, code = e.run("migrate", "abc12", "--output=json")
	if code != 1 {
		t.Fatalf("re-run exit %d; stderr=%s", code, stderr) // ORCIDs/contact still TODO
	}
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(r.Downloaded) != 0 || len(r.Skipped) != 3 {
		t.Errorf("re-run downloaded=%d skipped=%d, want 0/3", len(r.Downloaded), len(r.Skipped))
	}
	rewritten := e.readFile(".datapin/datapin.toml")
	if !strings.Contains(rewritten, "CC0-1.0") {
		t.Errorf("user-edited license was clobbered on re-run:\n%s", rewritten)
	}
}

func TestMigrate_GUIDMode_DryRun(t *testing.T) {
	e := newTestEnv(t)
	setupMigrateProject(e)

	stdout, stderr, code := e.run("migrate", "abc12", "--dry-run", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	var r migrateResult
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if !r.DryRun || len(r.Downloaded) != 3 {
		t.Errorf("dry_run=%v downloaded=%d, want true/3", r.DryRun, len(r.Downloaded))
	}
	for _, path := range []string{"data/raw/counts.h5", "docs/home.md", ".datapin/datapin.toml", "MIGRATED.md"} {
		if e.fileExists(path) {
			t.Errorf("dry-run must write nothing, but %s exists", path)
		}
	}
}

func TestMigrate_Components(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Parent")
	e.srv.AddFile("abc12", "/root.txt", []byte("root"))
	e.srv.AddComponent("abc12", "xyz34", "Sequencing Data")
	e.srv.AddFile("xyz34", "/seq/reads.fq", []byte("reads"))

	// Default: root only, with a notice naming the skipped component.
	stdout, stderr, code := e.run("migrate", "abc12", "--output=json")
	if code != 1 { // todos remain
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	var r migrateResult
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(r.ComponentsSkipped) != 1 || r.ComponentsSkipped[0] != "xyz34" {
		t.Errorf("components_skipped = %v", r.ComponentsSkipped)
	}
	if e.fileExists("sequencing-data/seq/reads.fq") {
		t.Error("component files must not be exported without --components")
	}

	// --components: each component becomes its own dataset under a prefix.
	e2 := newTestEnv(t)
	e2.srv.AddProject("abc12", "Parent")
	e2.srv.AddFile("abc12", "/root.txt", []byte("root"))
	e2.srv.AddComponent("abc12", "xyz34", "Sequencing Data")
	e2.srv.AddFile("xyz34", "/seq/reads.fq", []byte("reads"))

	stdout, stderr, code = e2.run("migrate", "abc12", "--components", "--output=json")
	if code != 1 {
		t.Fatalf("--components exit %d; stderr=%s", code, stderr)
	}
	var r2 migrateResult
	if err := json.Unmarshal([]byte(stdout), &r2); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(r2.Datasets) != 2 {
		t.Fatalf("datasets = %+v, want 2", r2.Datasets)
	}
	if len(r2.ComponentsSkipped) != 0 {
		t.Errorf("components_skipped = %v, want none", r2.ComponentsSkipped)
	}
	if len(r2.Downloaded) != 2 {
		t.Errorf("downloaded = %+v, want root.txt + component file", r2.Downloaded)
	}
	if got := e2.readFile("sequencing-data/seq/reads.fq"); got != "reads" {
		t.Errorf("component file = %q", got)
	}
	manifest := e2.readFile(".datapin/datapin.toml")
	for _, want := range []string{"sequencing-data", "https://osf.io/xyz34"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
}

// writeManifestModeFixture registers remote state and a schema-1 style
// manifest: one in-sync file, one missing locally (must be fetched), and one
// in-sync wiki page.
func writeManifestModeFixture(e *testEnv) {
	e.srv.AddProject("abc12", "Neuro Project")
	e.srv.AddContributor("abc12", "Ada Lovelace", "Ada", "Lovelace")
	e.srv.AddFile("abc12", "/data/a.csv", []byte("aaa"))
	e.srv.AddFile("abc12", "/results/r.txt", []byte("rrr"))
	e.srv.AddWiki("abc12", "home", []byte("home page"))

	e.writeFile("data/a.csv", "aaa")
	e.writeFile("docs/home.md", "home page")
	e.writeFile(".datapin/datapin.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local = "data/a.csv"
remote = "/data/a.csv"
version = 1
md5 = "%s"

[[files]]
local = "results/r.txt"
remote = "/results/r.txt"
version = 1
md5 = "%s"

[[wikis]]
local = "docs/home.md"
page = "home"
version = 1
md5 = "%s"
`, md5hex("aaa"), md5hex("rrr"), md5hex("home page")))
}

func TestMigrate_ManifestMode(t *testing.T) {
	e := newTestEnv(t)
	writeManifestModeFixture(e)

	stdout, stderr, code := e.run("migrate", "--yes", "--output=json")
	if code != 1 { // todos remain
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	var r migrateResult
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if r.Mode != "manifest" || r.Source != "abc12" {
		t.Errorf("header = %+v", r)
	}
	// The MISSING file was fetched so local is complete.
	if got := e.readFile("results/r.txt"); got != "rrr" {
		t.Errorf("results/r.txt = %q", got)
	}
	if len(r.Downloaded) != 1 || r.Downloaded[0].Path != "results/r.txt" {
		t.Errorf("downloaded = %+v", r.Downloaded)
	}
	// One dataset per top-level directory.
	if len(r.Datasets) != 2 || r.Datasets[0].Slug != "data" || r.Datasets[1].Slug != "results" {
		t.Errorf("datasets = %+v", r.Datasets)
	}

	manifest := e.readFile(".datapin/datapin.toml")
	if strings.Contains(manifest, "[[files]]") || strings.Contains(manifest, "[[wikis]]") {
		t.Errorf("migrated manifest still references OSF:\n%s", manifest)
	}
	for _, want := range []string{
		"[[datasets]]",
		"slug = 'data'",
		"slug = 'results'",
		"data/a.csv",
		"results/r.txt",
		"IsDerivedFrom",
		"https://osf.io/abc12",
		"Lovelace, Ada",
		"docs/home.md", // wiki converted to a site page
		"TODO",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
	if !e.fileExists("MIGRATED.md") {
		t.Error("MIGRATED.md not written")
	}
}

func TestMigrate_ManifestMode_DryRun(t *testing.T) {
	e := newTestEnv(t)
	writeManifestModeFixture(e)

	stdout, stderr, code := e.run("migrate", "--dry-run", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	var r migrateResult
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if !r.DryRun || len(r.Downloaded) != 1 || len(r.Datasets) != 2 {
		t.Errorf("plan = %+v", r)
	}
	if e.fileExists("results/r.txt") {
		t.Error("dry-run must not download")
	}
	if !strings.Contains(e.readFile(".datapin/datapin.toml"), "[[files]]") {
		t.Error("dry-run must not rewrite the manifest")
	}
	if e.fileExists("MIGRATED.md") {
		t.Error("dry-run must not write MIGRATED.md")
	}
}

func TestMigrate_ManifestMode_DivergedFailsHard(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Neuro Project")
	e.srv.AddFile("abc12", "/data/a.csv", []byte("one"))
	e.srv.AddVersion("abc12", "/data/a.csv", []byte("two")) // remote moved
	e.srv.AddFile("abc12", "/results/r.txt", []byte("rrr"))

	e.writeFile("data/a.csv", "three") // local moved too → DIVERGED
	e.writeFile(".datapin/datapin.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local = "data/a.csv"
remote = "/data/a.csv"
version = 1
md5 = "%s"

[[files]]
local = "results/r.txt"
remote = "/results/r.txt"
version = 1
md5 = "%s"
`, md5hex("one"), md5hex("rrr")))

	_, stderr, code := e.run("migrate", "--yes")
	if code == 0 {
		t.Fatal("diverged entry must fail the migration")
	}
	if !strings.Contains(stderr, "divergence") {
		t.Errorf("stderr = %s", stderr)
	}
	// The pre-flight fails before any transfer or rewrite.
	if e.fileExists("results/r.txt") {
		t.Error("no bytes may move when a divergence blocks the run")
	}
	if !strings.Contains(e.readFile(".datapin/datapin.toml"), "[[files]]") {
		t.Error("manifest must be untouched after a blocked run")
	}
}

func TestMigrate_ManifestMode_DatasetOverride(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Neuro Project")
	// Untracked-on-remote entries: version 0, local content only (NOT_PUSHED,
	// kept as-is — migrate never uploads).
	e.writeFile("data/raw/a.h5", "h5")
	e.writeFile("data/b.csv", "b")
	e.writeFile(".datapin/datapin.toml", `[project]
id = "abc12"

[[files]]
local = "data/raw/a.h5"
remote = "/data/raw/a.h5"
version = 0
md5 = ""

[[files]]
local = "data/b.csv"
remote = "/data/b.csv"
version = 0
md5 = ""
`)

	stdout, stderr, code := e.run("migrate", "--yes", "--dataset", "raw=data/raw/**", "--output=json")
	if code != 1 { // todos remain
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	var r migrateResult
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(r.Datasets) != 2 || r.Datasets[0].Slug != "raw" || r.Datasets[0].Files != 1 || r.Datasets[1].Slug != "data" {
		t.Errorf("datasets = %+v", r.Datasets)
	}
}

func TestMigrate_ArgumentValidation(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "P")

	// No manifest and no GUID → actionable error.
	_, stderr, code := e.run("migrate")
	if code == 0 || !strings.Contains(stderr, "GUID") {
		t.Errorf("bare migrate without a manifest: code=%d stderr=%s", code, stderr)
	}

	// --dataset is manifest-mode only.
	_, stderr, code = e.run("migrate", "abc12", "--dataset", "x=y")
	if code == 0 || !strings.Contains(stderr, "--dataset") {
		t.Errorf("--dataset with a GUID: code=%d stderr=%s", code, stderr)
	}

	// --components is GUID-mode only.
	e.writeFile(".datapin/datapin.toml", "[project]\nid = \"abc12\"\n\n[[files]]\nlocal = \"x\"\nremote = \"/x\"\nversion = 0\nmd5 = \"\"\n")
	_, stderr, code = e.run("migrate", "--components")
	if code == 0 || !strings.Contains(stderr, "--components") {
		t.Errorf("--components in manifest mode: code=%d stderr=%s", code, stderr)
	}

	// GUID mode refuses a dest that already tracks OSF via [[files]].
	_, stderr, code = e.run("migrate", "abc12")
	if code == 0 || !strings.Contains(stderr, "[[files]]") {
		t.Errorf("GUID mode into a [[files]] manifest: code=%d stderr=%s", code, stderr)
	}
}
