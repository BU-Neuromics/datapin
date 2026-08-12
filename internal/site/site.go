// Package site renders the project's static documentation site (plan
// §4.5): markdown pages through a pure-Go goldmark pipeline plus
// generated, citation-ready dataset landing pages with schema.org
// JSON-LD — the wiki replacement. Every stage is a function from inputs
// to bytes; disk and network stay in the callers.
package site

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"go.abhg.dev/goldmark/frontmatter"

	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/meta"
	"github.com/BU-Neuromics/datapin/internal/output"
)

//go:embed theme/*.tmpl theme/style.css
var themeFS embed.FS

var themeTemplates = template.Must(template.ParseFS(themeFS, "theme/*.tmpl"))

// BuildInput is everything Build needs, gathered by the command layer.
type BuildInput struct {
	Manifest *manifest.Manifest
	// PageSources maps each site page's Local path to its markdown bytes.
	PageSources map[string][]byte
	// FileSizes maps dataset-file local paths to sizes (0/absent = unknown).
	FileSizes map[string]int64
	// Citations maps dataset slug to a pre-fetched formatted citation
	// (DOI content negotiation, cached by the caller); absent slugs get a
	// locally rendered one.
	Citations map[string]string
	// Year stamps locally rendered citations (callers pass time.Now()).
	Year string
}

// Site is the rendered output: site-relative path → content.
type Site struct {
	Files map[string][]byte
}

// nav is the cross-page navigation model.
type nav struct {
	SiteTitle string
	Pages     []navItem
	Datasets  []navItem
}

type navItem struct {
	Title string
	Href  string
}

// renderedPage is one markdown page after conversion, held so the nav can
// be built from real titles before any page is written.
type renderedPage struct {
	Slug  string
	Title string
	Body  template.HTML
}

// Build renders the whole site.
func Build(in BuildInput) (Site, error) {
	m := in.Manifest
	title := m.Site.Title
	if title == "" {
		title = "datapin project"
	}

	// Render every markdown page up front: the nav labels pages by their
	// title, so no page can be rendered until all titles are known.
	pages := make([]renderedPage, 0, len(m.Site.Pages))
	for _, p := range m.Site.Pages {
		src, ok := in.PageSources[p.Local]
		if !ok {
			return Site{}, fmt.Errorf("site page %s: no source content", p.Local)
		}
		fmTitle, body, err := renderMarkdown(src)
		if err != nil {
			return Site{}, fmt.Errorf("rendering %s: %w", p.Local, err)
		}
		pageTitle := fmTitle
		if pageTitle == "" {
			pageTitle = p.Slug
		}
		pages = append(pages, renderedPage{Slug: p.Slug, Title: pageTitle, Body: body})
	}

	n := nav{SiteTitle: title}
	for _, p := range pages {
		if p.Slug == "index" {
			continue
		}
		n.Pages = append(n.Pages, navItem{Title: p.Title, Href: relRoot(p.Slug) + "/"})
	}
	for _, ds := range m.Datasets {
		dsTitle := ds.Metadata.Title
		if dsTitle == "" {
			dsTitle = ds.Slug
		}
		n.Datasets = append(n.Datasets, navItem{Title: dsTitle, Href: "datasets/" + ds.Slug + "/"})
	}

	files := map[string][]byte{}

	// Markdown pages.
	var indexBody template.HTML
	indexTitle := ""
	for _, p := range pages {
		if p.Slug == "index" {
			indexBody, indexTitle = p.Body, p.Title
			continue
		}
		html, err := renderPage(pageData{
			Nav: n, Title: p.Title, TabTitle: p.Title + " — " + title,
			Body: p.Body, Depth: 1,
		})
		if err != nil {
			return Site{}, err
		}
		files[p.Slug+"/index.html"] = html
	}

	// Dataset landing pages.
	for i := range m.Datasets {
		ds := &m.Datasets[i]
		citation := in.Citations[ds.Slug]
		if citation == "" && ds.VersionDOI != "" {
			citation = meta.LocalCitation(ds, in.Year)
		}
		html, err := renderDatasetPage(n, ds, in.FileSizes, citation)
		if err != nil {
			return Site{}, fmt.Errorf("rendering dataset %q page: %w", ds.Slug, err)
		}
		files["datasets/"+ds.Slug+"/index.html"] = html
	}

	// Index (site home): index.md content + dataset cards + DataCatalog.
	idx, err := renderIndex(n, m, indexTitle, indexBody, in)
	if err != nil {
		return Site{}, err
	}
	files["index.html"] = idx

	// Site furniture.
	css, err := themeFS.ReadFile("theme/style.css")
	if err != nil {
		return Site{}, err
	}
	files["style.css"] = css
	files[".nojekyll"] = []byte{}
	files["robots.txt"] = []byte("User-agent: *\nAllow: /\n")
	if m.Site.BaseURL != "" {
		files["sitemap.xml"] = sitemap(m.Site.BaseURL, files)
	}

	return Site{Files: files}, nil
}

// renderMarkdown converts one markdown source: GFM through goldmark with
// frontmatter parsed out (title honored). Raw HTML is escaped — page
// sources are trusted-ish, but generated sites should not be an XSS
// vector by default.
func renderMarkdown(src []byte) (title string, body template.HTML, err error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, &frontmatter.Extender{}),
		// Without ids on headings no in-page anchor resolves — a page
		// carrying its own table of contents would link nowhere, and so
		// would every cross-page link to a named section.
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	var buf bytes.Buffer
	ctx := parser.NewContext()
	if err := md.Convert(src, &buf, parser.WithContext(ctx)); err != nil {
		return "", "", err
	}
	if fm := frontmatter.Get(ctx); fm != nil {
		var data struct {
			Title string `yaml:"title"`
		}
		if err := fm.Decode(&data); err == nil {
			title = data.Title
		}
	}
	return title, template.HTML(buf.String()), nil //nolint:gosec // goldmark escapes raw HTML by default
}

type pageData struct {
	Nav      nav
	Title    string
	TabTitle string
	Body     template.HTML
	// Depth is how many directories below the site root the page lives,
	// for relative asset/nav hrefs.
	Depth  int
	JSONLD template.JS
	Extra  any
}

func renderPage(d pageData) ([]byte, error) {
	var buf bytes.Buffer
	if err := themeTemplates.ExecuteTemplate(&buf, "page.tmpl", withRel(d)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// relData decorates pageData with the relative prefix to the site root.
type relData struct {
	pageData
	Root string
}

func withRel(d pageData) relData {
	return relData{pageData: d, Root: strings.Repeat("../", d.Depth)}
}

func relRoot(slug string) string { return slug }

// datasetView is the landing-page template model.
type datasetView struct {
	Slug       string
	Title      string
	Descr      string
	License    string
	Keywords   string
	VersionDOI string
	ConceptDOI string
	Version    int
	Published  bool
	Citation   string
	Creators   []creatorView
	Files      []fileView
}

type creatorView struct {
	Name  string
	ORCID string
}

type fileView struct {
	Key  string
	Size string
	MD5  string
}

func renderDatasetPage(n nav, ds *manifest.Dataset, sizes map[string]int64, citation string) ([]byte, error) {
	v := datasetView{
		Slug: ds.Slug, Title: ds.Metadata.Title, Descr: ds.Metadata.Description,
		License: ds.Metadata.License, Keywords: strings.Join(ds.Metadata.Keywords, ", "),
		VersionDOI: ds.VersionDOI, ConceptDOI: ds.ConceptDOI, Version: ds.Version,
		Published: ds.Record != "", Citation: citation,
	}
	if v.Title == "" {
		v.Title = ds.Slug
	}
	for _, c := range ds.Metadata.Creators {
		v.Creators = append(v.Creators, creatorView{Name: c.Name, ORCID: meta.NormalizeORCID(c.ORCID)})
	}
	for _, f := range ds.Files {
		size := ""
		if sz := sizes[f.Local]; sz > 0 {
			size = output.FormatSize(sz)
		}
		v.Files = append(v.Files, fileView{Key: f.Key, Size: size, MD5: f.MD5})
	}

	ld, err := datasetJSONLD(ds, sizes)
	if err != nil {
		return nil, err
	}

	var body bytes.Buffer
	if err := themeTemplates.ExecuteTemplate(&body, "dataset.tmpl", v); err != nil {
		return nil, err
	}
	return renderPage(pageData{
		Nav: n, Title: v.Title, TabTitle: v.Title + " — " + n.SiteTitle,
		Body: template.HTML(body.String()), Depth: 2, JSONLD: template.JS(ld), //nolint:gosec // ld is json.Marshal output
	})
}

// datasetJSONLD renders the schema.org/Dataset block Google Dataset
// Search reads (plan §2.2).
func datasetJSONLD(ds *manifest.Dataset, sizes map[string]int64) ([]byte, error) {
	md := ds.Metadata
	ld := map[string]any{
		"@context": "https://schema.org/",
		"@type":    "Dataset",
		"name":     md.Title,
	}
	if md.Description != "" {
		ld["description"] = md.Description
	}
	if ds.VersionDOI != "" {
		ld["identifier"] = "https://doi.org/" + ds.VersionDOI
		ld["url"] = "https://doi.org/" + ds.VersionDOI
	}
	if ds.Version > 0 {
		ld["version"] = fmt.Sprintf("%d", ds.Version)
	}
	if md.License != "" {
		ld["license"] = "https://spdx.org/licenses/" + md.License + ".html"
	}
	if len(md.Keywords) > 0 {
		ld["keywords"] = strings.Join(md.Keywords, ", ")
	}
	var creators []any
	for _, c := range md.Creators {
		p := map[string]any{"@type": "Person", "name": c.Name}
		if orcid := meta.NormalizeORCID(c.ORCID); orcid != "" {
			p["@id"] = "https://orcid.org/" + orcid
		}
		creators = append(creators, p)
	}
	if len(creators) > 0 {
		ld["creator"] = creators
	}
	var dist []any
	for _, f := range ds.Files {
		d := map[string]any{"@type": "DataDownload", "name": f.Key}
		if sz := sizes[f.Local]; sz > 0 {
			d["contentSize"] = fmt.Sprintf("%d", sz)
		}
		dist = append(dist, d)
	}
	if len(dist) > 0 {
		ld["distribution"] = dist
	}
	return json.MarshalIndent(ld, "", "  ")
}

// renderIndex renders the home page with dataset cards and a DataCatalog
// JSON-LD block.
func renderIndex(n nav, m *manifest.Manifest, pageTitle string, body template.HTML, in BuildInput) ([]byte, error) {
	type card struct {
		Slug, Title, Descr, DOI string
		Published               bool
	}
	var cards []card
	catalog := map[string]any{
		"@context": "https://schema.org/",
		"@type":    "DataCatalog",
		"name":     n.SiteTitle,
	}
	var catalogSets []any
	for i := range m.Datasets {
		ds := &m.Datasets[i]
		t := ds.Metadata.Title
		if t == "" {
			t = ds.Slug
		}
		cards = append(cards, card{
			Slug: ds.Slug, Title: t, Descr: ds.Metadata.Description,
			DOI: ds.VersionDOI, Published: ds.Record != "",
		})
		set := map[string]any{"@type": "Dataset", "name": t}
		if ds.VersionDOI != "" {
			set["identifier"] = "https://doi.org/" + ds.VersionDOI
		}
		catalogSets = append(catalogSets, set)
	}
	if len(catalogSets) > 0 {
		catalog["dataset"] = catalogSets
	}
	ld, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return nil, err
	}

	var idx bytes.Buffer
	if err := themeTemplates.ExecuteTemplate(&idx, "home.tmpl", map[string]any{
		"Body":  body,
		"Cards": cards,
	}); err != nil {
		return nil, err
	}
	tab := n.SiteTitle
	if pageTitle != "" {
		tab = pageTitle + " — " + n.SiteTitle
	}
	return renderPage(pageData{
		Nav: n, Title: n.SiteTitle, TabTitle: tab,
		Body: template.HTML(idx.String()), Depth: 0, JSONLD: template.JS(ld), //nolint:gosec // ld is json.Marshal output
	})
}

// sitemap lists every index.html under baseURL.
func sitemap(baseURL string, files map[string][]byte) []byte {
	base := strings.TrimSuffix(baseURL, "/")
	var urls []string
	for path := range files {
		switch {
		case path == "index.html":
			urls = append(urls, base+"/")
		case strings.HasSuffix(path, "/index.html"):
			urls = append(urls, base+"/"+strings.TrimSuffix(path, "index.html"))
		}
	}
	sort.Strings(urls)
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range urls {
		fmt.Fprintf(&b, "  <url><loc>%s</loc></url>\n", u)
	}
	b.WriteString("</urlset>\n")
	return b.Bytes()
}
