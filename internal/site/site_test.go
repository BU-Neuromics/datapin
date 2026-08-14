package site_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/site"
)

func testManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Schema:  2,
		Project: manifest.ProjectConfig{ID: "abc12"},
		Site: manifest.SiteConfig{
			Title:   "Cortical RNA-seq",
			BaseURL: "https://org.github.io/cortical",
			Pages: []manifest.SitePage{
				{Local: "docs/index.md", Slug: "index"},
				{Local: "docs/methods.md", Slug: "methods"},
			},
		},
		Datasets: []manifest.Dataset{{
			Slug:       "counts",
			Record:     "585301",
			Version:    2,
			VersionDOI: "10.5072/zenodo.585301",
			ConceptDOI: "10.5072/zenodo.585299",
			Metadata: manifest.DatasetMetadata{
				Title:       "Aligned counts",
				Description: "RNA-seq count matrices",
				License:     "CC0-1.0",
				Keywords:    []string{"RNA-seq", "cortex"},
				Creators: []manifest.DatasetCreator{
					{Name: "Labadorf, Adam", ORCID: "0000-0002-1825-0097"},
				},
			},
			Files: []manifest.DatasetFile{
				{Local: "results/counts.h5", Key: "counts.h5", MD5: "aabbcc"},
			},
		}},
	}
}

func buildInput() site.BuildInput {
	return site.BuildInput{
		Manifest: testManifest(),
		PageSources: map[string][]byte{
			"docs/index.md":   []byte("---\ntitle: Home\n---\n\n# Welcome\n\nSome *markdown*."),
			"docs/methods.md": []byte("# Methods\n\n- step one\n- step two"),
		},
		FileSizes: map[string]int64{"results/counts.h5": 123456},
	}
}

func TestBuild_Pages(t *testing.T) {
	out, err := site.Build(buildInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	index, ok := out.Files["index.html"]
	if !ok {
		t.Fatalf("no index.html; files: %v", keys(out.Files))
	}
	if !strings.Contains(string(index), "Cortical RNA-seq") {
		t.Error("index must carry the site title")
	}
	if !strings.Contains(string(index), "Welcome") {
		t.Error("index page content must come from docs/index.md")
	}

	methods, ok := out.Files["methods/index.html"]
	if !ok {
		t.Fatalf("no methods page; files: %v", keys(out.Files))
	}
	if !strings.Contains(string(methods), "<li>step one</li>") {
		t.Errorf("markdown not rendered:\n%s", methods)
	}

	if _, ok := out.Files["style.css"]; !ok {
		t.Error("theme stylesheet missing")
	}
}

func TestBuild_DatasetLandingPage(t *testing.T) {
	out, err := site.Build(buildInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	page, ok := out.Files["datasets/counts/index.html"]
	if !ok {
		t.Fatalf("no dataset landing page; files: %v", keys(out.Files))
	}
	html := string(page)
	for _, want := range []string{
		"Aligned counts",
		"10.5072/zenodo.585301", // version DOI
		"10.5072/zenodo.585299", // concept DOI
		"counts.h5",
		"aabbcc",   // checksum
		"CC0-1.0",  // license
		"Labadorf", // creator
	} {
		if !strings.Contains(html, want) {
			t.Errorf("landing page missing %q", want)
		}
	}

	// schema.org/Dataset JSON-LD, parseable and typed.
	ldRe := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	match := ldRe.FindStringSubmatch(html)
	if match == nil {
		t.Fatal("no JSON-LD block on the landing page")
	}
	var ld map[string]any
	if err := json.Unmarshal([]byte(match[1]), &ld); err != nil {
		t.Fatalf("JSON-LD is not valid JSON: %v", err)
	}
	if ld["@type"] != "Dataset" {
		t.Errorf("@type = %v", ld["@type"])
	}
	if ld["identifier"] != "https://doi.org/10.5072/zenodo.585301" {
		t.Errorf("identifier = %v", ld["identifier"])
	}
}

func TestBuild_IndexCatalogAndSitemap(t *testing.T) {
	out, err := site.Build(buildInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	index := string(out.Files["index.html"])
	if !strings.Contains(index, `"@type": "DataCatalog"`) && !strings.Contains(index, `"@type":"DataCatalog"`) {
		t.Error("index must embed DataCatalog JSON-LD")
	}
	if !strings.Contains(index, "datasets/counts/") {
		t.Error("index must link the dataset landing page")
	}

	sm, ok := out.Files["sitemap.xml"]
	if !ok {
		t.Fatal("no sitemap.xml")
	}
	if !strings.Contains(string(sm), "https://org.github.io/cortical/datasets/counts/") {
		t.Errorf("sitemap missing dataset URL:\n%s", sm)
	}
	if _, ok := out.Files["robots.txt"]; !ok {
		t.Error("no robots.txt")
	}
	if _, ok := out.Files[".nojekyll"]; !ok {
		t.Error("no .nojekyll (GitHub Pages must not run Jekyll)")
	}
}

// Headings must carry ids, or no in-page anchor works: a page with its own
// table of contents (the troubleshooting reference) would link nowhere, and
// so would every cross-page link to a specific section.
func TestBuild_HeadingsGetAnchorIDs(t *testing.T) {
	in := buildInput()
	in.PageSources["docs/methods.md"] = []byte(
		"# Methods\n\n## Publish refuses: a license is required\n\ntext\n\n## Step two\n\nmore\n")

	out, err := site.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	html := string(out.Files["methods/index.html"])
	for _, want := range []string{
		`id="publish-refuses-a-license-is-required"`,
		`id="step-two"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing heading anchor %s in:\n%s", want, html)
		}
	}
}

func TestBuild_UnsafeHTMLEscaped(t *testing.T) {
	in := buildInput()
	in.PageSources["docs/index.md"] = []byte("# Hi\n\n<script>alert(1)</script>")
	out, err := site.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(string(out.Files["index.html"]), "<script>alert(1)</script>") {
		t.Error("raw HTML in markdown must not pass through unescaped")
	}
}

// The nav must name pages the way a reader does — a slug like "first-doi"
// is a URL component, not a link label. Frontmatter title wins; the slug is
// only the fallback for a page that declares none.
func TestBuild_NavUsesPageTitles(t *testing.T) {
	in := buildInput()
	in.Manifest.Site.Pages = append(in.Manifest.Site.Pages,
		manifest.SitePage{Local: "docs/first-doi.md", Slug: "first-doi"})
	in.PageSources["docs/first-doi.md"] = []byte("---\ntitle: Your first DOI\n---\n\n# Your first DOI\n\nGo.")

	out, err := site.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, page := range []string{"methods/index.html", "first-doi/index.html", "index.html"} {
		html := string(out.Files[page])
		if !strings.Contains(html, `>Your first DOI</a>`) {
			t.Errorf("%s: nav must label the page with its title, not its slug:\n%s", page, navOf(html))
		}
		if strings.Contains(navOf(html), `>first-doi</a>`) {
			t.Errorf("%s: nav still shows the raw slug:\n%s", page, navOf(html))
		}
	}

	// A page with no frontmatter title keeps falling back to its slug.
	if !strings.Contains(navOf(string(out.Files["index.html"])), `>methods</a>`) {
		t.Error("a page without a frontmatter title should fall back to its slug in the nav")
	}
}

func navOf(html string) string {
	if _, rest, ok := strings.Cut(html, "<nav>"); ok {
		if inner, _, ok := strings.Cut(rest, "</nav>"); ok {
			return inner
		}
	}
	return html
}

func TestBuild_FrontmatterTitleWins(t *testing.T) {
	out, err := site.Build(buildInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(string(out.Files["index.html"]), "<title>Home — Cortical RNA-seq</title>") {
		t.Error("frontmatter title should drive the page <title>")
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
