package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

const v2TOML = `
schema = 2

[project]
id = "abc12"
default_archive = "sandbox"

[[datasets]]
slug    = "counts"
record  = "585301"
concept = "585299"
concept_doi = "10.5072/zenodo.585299"
version     = 2
version_doi = "10.5072/zenodo.585301"

  [datasets.metadata]
  title       = "Aligned counts"
  description = "RNA-seq count matrices"
  license     = "CC0-1.0"
  keywords    = ["RNA-seq"]
  resource_type = "dataset"
  [[datasets.metadata.creators]]
  name  = "Labadorf, Adam"
  orcid = "0000-0002-1234-5678"

  [[datasets.files]]
  local = "results/counts.h5"
  key   = "counts.h5"
  md5   = "d41d8cd98f00b204e9800998ecf8427e"
  [[datasets.files]]
  local = "results/coldata.csv"
  md5   = ""

[[datasets]]
slug    = "figures"
archive = "production"

  [[datasets.files]]
  local = "figs/f1.png"
`

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "datapin.toml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadV2_Datasets(t *testing.T) {
	m, err := manifest.Load(writeManifest(t, v2TOML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Schema != 2 {
		t.Errorf("Schema = %d, want 2", m.Schema)
	}
	if len(m.Datasets) != 2 {
		t.Fatalf("datasets = %d, want 2", len(m.Datasets))
	}
	d := m.Datasets[0]
	if d.Slug != "counts" || d.Record != "585301" || d.Version != 2 ||
		d.ConceptDOI != "10.5072/zenodo.585299" || d.VersionDOI != "10.5072/zenodo.585301" {
		t.Errorf("dataset[0] = %+v", d)
	}
	if d.Metadata.Title != "Aligned counts" || d.Metadata.License != "CC0-1.0" {
		t.Errorf("metadata = %+v", d.Metadata)
	}
	if len(d.Metadata.Creators) != 1 || d.Metadata.Creators[0].ORCID != "0000-0002-1234-5678" {
		t.Errorf("creators = %+v", d.Metadata.Creators)
	}
	if len(d.Files) != 2 || d.Files[0].Key != "counts.h5" {
		t.Errorf("files = %+v", d.Files)
	}
}

func TestLoadV2_DefaultKeyIsBasename(t *testing.T) {
	m, err := manifest.Load(writeManifest(t, v2TOML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// files[1] omits key → derived from the local basename (D24).
	if got := m.Datasets[0].Files[1].Key; got != "coldata.csv" {
		t.Errorf("derived key = %q, want coldata.csv", got)
	}
}

func TestDataset_ResolveArchive(t *testing.T) {
	m, err := manifest.Load(writeManifest(t, v2TOML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := m.Datasets[0].ResolveArchive(m.Project.DefaultArchive); got != "sandbox" {
		t.Errorf("ResolveArchive = %q, want default sandbox", got)
	}
	if got := m.Datasets[1].ResolveArchive(m.Project.DefaultArchive); got != "production" {
		t.Errorf("ResolveArchive = %q, want override production", got)
	}
}

func TestLoadV2_SchemaOneStillLoads(t *testing.T) {
	m, err := manifest.Load(writeManifest(t, validTOML))
	if err != nil {
		t.Fatalf("Load schema-1: %v", err)
	}
	if m.Schema != 0 && m.Schema != 1 {
		t.Errorf("Schema = %d for a legacy manifest", m.Schema)
	}
	if len(m.Datasets) != 0 {
		t.Errorf("legacy manifest must have no datasets")
	}
}

func TestLoadV2_UnknownSchemaRejected(t *testing.T) {
	_, err := manifest.Load(writeManifest(t, "schema = 3\n[project]\nid = \"abc12\"\n"))
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("Load schema=3 = %v, want unsupported-schema error", err)
	}
}

func TestValidateV2_DuplicateSlug(t *testing.T) {
	bad := `
[project]
id = "abc12"
[[datasets]]
slug = "counts"
[[datasets]]
slug = "counts"
`
	_, err := manifest.Load(writeManifest(t, bad))
	if err == nil || !strings.Contains(err.Error(), "slug") {
		t.Fatalf("duplicate slug = %v, want error", err)
	}
}

func TestValidateV2_EmptySlug(t *testing.T) {
	bad := "[project]\nid = \"abc12\"\n[[datasets]]\nslug = \"\"\n"
	_, err := manifest.Load(writeManifest(t, bad))
	if err == nil || !strings.Contains(err.Error(), "slug") {
		t.Fatalf("empty slug = %v, want error", err)
	}
}

func TestValidateV2_DuplicateLocalAcrossSections(t *testing.T) {
	bad := `
[project]
id = "abc12"
[[files]]
local = "data/x.csv"
remote = "/x.csv"
version = 1
md5 = "aa"
[[datasets]]
slug = "d"
[[datasets.files]]
local = "data/x.csv"
`
	_, err := manifest.Load(writeManifest(t, bad))
	if err == nil || !strings.Contains(err.Error(), "duplicate local") {
		t.Fatalf("cross-section duplicate local = %v, want error", err)
	}
}

func TestValidateV2_DuplicateKeyInDataset(t *testing.T) {
	bad := `
[project]
id = "abc12"
[[datasets]]
slug = "d"
[[datasets.files]]
local = "a/data.csv"
key = "data.csv"
[[datasets.files]]
local = "b/data.csv"
key = "data.csv"
`
	_, err := manifest.Load(writeManifest(t, bad))
	if err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("duplicate key = %v, want error", err)
	}
}

func TestSaveV2_RoundTrip(t *testing.T) {
	p := writeManifest(t, v2TOML)
	m, err := manifest.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, ".datapin", "datapin.toml")
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Save(m, out); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m2, err := manifest.Load(out)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if m2.Schema != 2 {
		t.Errorf("saved schema = %d, want 2", m2.Schema)
	}
	if len(m2.Datasets) != 2 || m2.Datasets[0].Slug != "counts" ||
		m2.Datasets[0].Files[1].Key != "coldata.csv" {
		t.Errorf("round-trip lost data: %+v", m2.Datasets)
	}
}

func TestSaveV2_LegacyManifestUpgradesSchemaField(t *testing.T) {
	p := writeManifest(t, validTOML)
	m, err := manifest.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "datapin.toml")
	if err := manifest.Save(m, out); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m2, err := manifest.Load(out)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if m2.Schema != 2 {
		t.Errorf("Save must stamp schema = 2, got %d", m2.Schema)
	}
	if len(m2.Files) != len(m.Files) {
		t.Errorf("files lost in upgrade: %d → %d", len(m.Files), len(m2.Files))
	}
}

func TestFindDataset(t *testing.T) {
	m, err := manifest.Load(writeManifest(t, v2TOML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if d := m.FindDataset("figures"); d == nil || d.Slug != "figures" {
		t.Errorf("FindDataset(figures) = %+v", d)
	}
	if d := m.FindDataset("nope"); d != nil {
		t.Errorf("FindDataset(nope) = %+v, want nil", d)
	}
}

const siteTOML = `
[project]
id = "abc12"

[site]
title    = "Cortical RNA-seq"
base_url = "https://bu-neuromics.github.io/cortical"
deploy   = "gh-pages"
repo     = "BU-Neuromics/cortical"

[[site.pages]]
local = "docs/index.md"
slug  = "index"
[[site.pages]]
local = "docs/methods.md"
slug  = "methods"
`

func TestLoadV2_SiteConfig(t *testing.T) {
	m, err := manifest.Load(writeManifest(t, siteTOML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Site.Title != "Cortical RNA-seq" || m.Site.Deploy != "gh-pages" || m.Site.Repo != "BU-Neuromics/cortical" {
		t.Errorf("site = %+v", m.Site)
	}
	if len(m.Site.Pages) != 2 || m.Site.Pages[1].Slug != "methods" {
		t.Errorf("pages = %+v", m.Site.Pages)
	}
}

func TestLoadV2_SitePageSlugDefaultsToBasename(t *testing.T) {
	toml := `
[project]
id = "abc12"
[site]
title = "T"
[[site.pages]]
local = "docs/methods.md"
`
	m, err := manifest.Load(writeManifest(t, toml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Site.Pages[0].Slug != "methods" {
		t.Errorf("slug = %q, want basename without extension", m.Site.Pages[0].Slug)
	}
}

func TestValidateV2_SitePages(t *testing.T) {
	dupSlug := `
[project]
id = "abc12"
[site]
title = "T"
[[site.pages]]
local = "docs/a.md"
slug = "x"
[[site.pages]]
local = "docs/b.md"
slug = "x"
`
	if _, err := manifest.Load(writeManifest(t, dupSlug)); err == nil {
		t.Error("duplicate page slug must be rejected")
	}
	dupLocal := `
[project]
id = "abc12"
[[files]]
local = "docs/a.md"
remote = "/a.md"
version = 1
md5 = "aa"
[site]
title = "T"
[[site.pages]]
local = "docs/a.md"
`
	if _, err := manifest.Load(writeManifest(t, dupLocal)); err == nil {
		t.Error("page local colliding with a files entry must be rejected")
	}
}
