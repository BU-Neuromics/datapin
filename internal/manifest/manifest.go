package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/BU-Neuromics/datapin/internal/log"
)

// Manifest is the in-memory representation of datapin.toml.
//
// Schema 2 adds [[datasets]] (archive publication, plan §4.3) on top of the
// schema-1 [[files]]/[[wikis]] workspace sections, which remain valid and
// unchanged (D13). A manifest without a schema key is schema 1.
type Manifest struct {
	Schema   int           `toml:"schema,omitempty"`
	Project  ProjectConfig `toml:"project"`
	Files    []Entry       `toml:"files"`
	Wikis    []WikiEntry   `toml:"wikis,omitempty"`
	Datasets []Dataset     `toml:"datasets,omitempty"`
	Site     SiteConfig    `toml:"site,omitempty"`
}

// SiteConfig drives the static documentation site (plan §4.5): markdown
// pages plus generated dataset landing pages, deployed to GitHub Pages.
type SiteConfig struct {
	Title string `toml:"title,omitempty"`
	// BaseURL is the published site root (for sitemap/JSON-LD absolute
	// URLs), e.g. https://org.github.io/repo.
	BaseURL string `toml:"base_url,omitempty"`
	// Deploy is "gh-pages" (orphan-commit force-push) or "dir".
	Deploy string     `toml:"deploy,omitempty"`
	Repo   string     `toml:"repo,omitempty"` // owner/repo for gh-pages; default: origin
	Pages  []SitePage `toml:"pages,omitempty"`
}

// SitePage is one local markdown file rendered into the site.
type SitePage struct {
	Local string `toml:"local"`
	Slug  string `toml:"slug,omitempty"` // defaults to the basename sans extension
}

// ProjectConfig holds the default project GUID and the default archive
// remote datasets publish to.
type ProjectConfig struct {
	ID             string `toml:"id"`
	DefaultArchive string `toml:"default_archive,omitempty"`
}

// Dataset is one publishable record: a named group of files that publish
// together as a DOI-carrying archive version (one dataset = one record).
type Dataset struct {
	Slug string `toml:"slug"`
	// Archive names the configured archive remote; empty = the project's
	// default_archive.
	Archive string `toml:"archive,omitempty"`
	// Record is the latest published version's record id ("" until the
	// first publish); Concept is the parent id grouping all versions (D21).
	Record     string          `toml:"record"`
	Concept    string          `toml:"concept,omitempty"`
	ConceptDOI string          `toml:"concept_doi"`
	Version    int             `toml:"version"`
	VersionDOI string          `toml:"version_doi"`
	Metadata   DatasetMetadata `toml:"metadata,omitempty"`
	Files      []DatasetFile   `toml:"files"`
}

// ResolveArchive returns the archive remote name for this dataset.
func (d Dataset) ResolveArchive(defaultArchive string) string {
	if d.Archive != "" {
		return d.Archive
	}
	return defaultArchive
}

// DatasetFile pins one file of a dataset: a local path, its flat key on
// the record, and the MD5 of the pinned published version ("" if the file
// has never been published).
type DatasetFile struct {
	Local string `toml:"local"`
	Key   string `toml:"key"`
	MD5   string `toml:"md5"`
}

// DatasetMetadata is the publish-boundary metadata block (linted by
// `datapin check`; enforced only when publishing — a metadata-less dataset
// is valid while unpublished, plan §4.7).
type DatasetMetadata struct {
	Title        string           `toml:"title,omitempty"`
	Description  string           `toml:"description,omitempty"`
	License      string           `toml:"license,omitempty"`
	Keywords     []string         `toml:"keywords,omitempty"`
	ResourceType string           `toml:"resource_type,omitempty"`
	Publisher    string           `toml:"publisher,omitempty"`
	Creators     []DatasetCreator `toml:"creators,omitempty"`
	Related      []RelatedID      `toml:"related,omitempty"`
}

// DatasetCreator is one author: display name "Family, Given", optional
// bare ORCID and affiliation.
type DatasetCreator struct {
	Name        string `toml:"name"`
	ORCID       string `toml:"orcid,omitempty"`
	Affiliation string `toml:"affiliation,omitempty"`
}

// RelatedID is a typed cross-link (DataCite relationType semantics).
type RelatedID struct {
	Identifier string `toml:"identifier"`
	Relation   string `toml:"relation"`
}

// FindDataset returns the dataset with the given slug, or nil.
func (m *Manifest) FindDataset(slug string) *Dataset {
	for i := range m.Datasets {
		if m.Datasets[i].Slug == slug {
			return &m.Datasets[i]
		}
	}
	return nil
}

// Entry describes one file tracked by the manifest.
//
// There is deliberately no direction field: what a transfer should do is
// decided at the moment of the transfer from the three-way comparison of local
// content, the pinned baseline, and the remote (see ClassifyFile). A standing
// per-entry default recorded weeks earlier cannot know that, and the field's
// only observable effect was to block transfers that were unambiguously safe
// (issue #81). Manifests that still carry the key load fine; it is ignored.
type Entry struct {
	Local   string `toml:"local"`
	Remote  string `toml:"remote"`
	Version int    `toml:"version"`
	MD5     string `toml:"md5"`
	Project string `toml:"project,omitempty"`
}

// ResolveProject returns the project GUID for this entry: the entry's own
// Project field if set, otherwise the manifest's default project ID.
func (e Entry) ResolveProject(defaultID string) string {
	if e.Project != "" {
		return e.Project
	}
	return defaultID
}

// WikiEntry describes one wiki page tracked by the manifest. It carries the
// same pinned baseline as a file entry (version + md5), but the remote side is
// a named wiki page rather than a storage path. The MD5 is computed by datapin
// from the page content (OSF exposes no content hash for wiki versions).
type WikiEntry struct {
	Local   string `toml:"local"`
	Page    string `toml:"page"`
	Version int    `toml:"version"`
	MD5     string `toml:"md5"`
	Project string `toml:"project,omitempty"`
}

// ResolveProject returns the project GUID for this wiki entry.
func (w WikiEntry) ResolveProject(defaultID string) string {
	if w.Project != "" {
		return w.Project
	}
	return defaultID
}

// BaselineEntry adapts the wiki entry's pinned baseline to the Entry shape so
// it flows through ClassifyFile unchanged — the state machine only reads
// Version and MD5.
func (w WikiEntry) BaselineEntry() Entry {
	return Entry{Version: w.Version, MD5: w.MD5}
}

// NotFoundError is returned by FindManifest when no .datapin/datapin.toml is found.
type NotFoundError struct{}

func (NotFoundError) Error() string {
	return ".datapin/datapin.toml not found in this directory or any parent"
}

// IsNotFound reports whether err is a NotFoundError.
func IsNotFound(err error) bool {
	_, ok := err.(NotFoundError)
	return ok
}

// Load parses and validates datapin.toml at path.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if n := LegacyDirectionCount(data); n > 0 {
		log.Warnf("%s: 'direction' is no longer used (%d entr%s) and will be dropped when the manifest is next written — "+
			"datapin now decides each transfer from local/pinned/remote state; run 'datapin status' to see it",
			path, n, plural(n, "y", "ies"))
	}

	if m.Schema > 2 {
		return nil, fmt.Errorf("%s: unsupported manifest schema %d (this datapin understands schema ≤ 2 — upgrade datapin)", path, m.Schema)
	}

	// Convenience default (D24): a dataset file with no explicit key gets
	// its local basename.
	for di := range m.Datasets {
		for fi := range m.Datasets[di].Files {
			if m.Datasets[di].Files[fi].Key == "" {
				m.Datasets[di].Files[fi].Key = filepath.Base(m.Datasets[di].Files[fi].Local)
			}
		}
	}
	// Same convenience for site pages: docs/methods.md → slug "methods".
	for pi := range m.Site.Pages {
		if m.Site.Pages[pi].Slug == "" {
			base := filepath.Base(m.Site.Pages[pi].Local)
			m.Site.Pages[pi].Slug = strings.TrimSuffix(base, filepath.Ext(base))
		}
	}

	if err := validate(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// LegacyDirectionCount reports how many entries in raw manifest bytes still
// carry the retired `direction` key. Unparseable input reports 0 — Load surfaces
// the parse error itself, and the warning must never be the thing that fails.
func LegacyDirectionCount(data []byte) int {
	var probe struct {
		Files []struct {
			Direction string `toml:"direction"`
		} `toml:"files"`
		Wikis []struct {
			Direction string `toml:"direction"`
		} `toml:"wikis"`
	}
	if err := toml.Unmarshal(data, &probe); err != nil {
		return 0
	}
	n := 0
	for _, f := range probe.Files {
		if f.Direction != "" {
			n++
		}
	}
	for _, w := range probe.Wikis {
		if w.Direction != "" {
			n++
		}
	}
	return n
}

// plural picks a suffix for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// IsLegacyPath reports whether path is a pre-rename .gosf/gosf.toml
// manifest. Legacy manifests are accepted read-only for migration.
func IsLegacyPath(path string) bool {
	return filepath.Base(path) == "gosf.toml" &&
		filepath.Base(filepath.Dir(path)) == ".gosf"
}

// Save writes the manifest to path atomically (temp file + rename).
// The parent directory is created if it does not exist.
// Legacy .gosf/gosf.toml manifests are read-only: Save refuses them with a
// migration hint rather than perpetuating the pre-rename layout.
func Save(m *Manifest, path string) error {
	if IsLegacyPath(path) {
		return fmt.Errorf("legacy manifest %s is read-only — migrate it first: mv .gosf .datapin && mv .datapin/gosf.toml .datapin/datapin.toml", path)
	}
	m.Schema = 2 // every write is current-schema
	data, err := toml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshalling manifest: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating manifest directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".datapin.toml.tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// FindManifest walks up from the current working directory until it finds
// .datapin/datapin.toml, or — for migration — a legacy .gosf/gosf.toml
// (accepted read-only, with a warning; the new name wins when both exist
// in the same directory). Returns (manifestPath, repoRoot, error).
// Returns NotFoundError if none is found.
func FindManifest() (string, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}

	for {
		candidate := filepath.Join(dir, ".datapin", "datapin.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, dir, nil
		}
		legacy := filepath.Join(dir, ".gosf", "gosf.toml")
		if _, err := os.Stat(legacy); err == nil {
			log.Warnf("found legacy %s — it is read-only; migrate it: mv .gosf .datapin && mv .datapin/gosf.toml .datapin/datapin.toml", legacy)
			return legacy, dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			return "", "", NotFoundError{}
		}
		dir = parent
	}
}

// Init creates or updates .datapin/datapin.toml in dir with the given project ID.
// The .datapin/ subdirectory is created if it does not exist.
// If the file exists, [project].id is updated and all [[files]] entries are preserved.
// created reports whether a new file was created.
func Init(dir, projectID string) (path string, created bool, err error) {
	datapinDir := filepath.Join(dir, ".datapin")
	if mkErr := os.MkdirAll(datapinDir, 0755); mkErr != nil {
		return "", false, fmt.Errorf("creating .datapin directory: %w", mkErr)
	}
	path = filepath.Join(datapinDir, "datapin.toml")

	var m Manifest
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		created = true
	} else {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return path, false, readErr
		}
		if parseErr := toml.Unmarshal(data, &m); parseErr != nil {
			return path, false, fmt.Errorf("parsing %s: %w", path, parseErr)
		}
	}

	m.Project.ID = projectID
	return path, created, Save(&m, path)
}

// validate checks all manifest invariants.
func validate(m *Manifest) error {
	seenLocal := make(map[string]bool)
	seenRemote := make(map[string]bool) // key: "project|remote"

	for i, f := range m.Files {
		// Resolve project
		proj := f.ResolveProject(m.Project.ID)
		if proj == "" {
			return fmt.Errorf("files[%d] (%q): no project GUID — set [project].id or per-entry project field",
				i, f.Local)
		}

		// No duplicate local paths
		if seenLocal[f.Local] {
			return fmt.Errorf("duplicate local path %q in manifest", f.Local)
		}
		seenLocal[f.Local] = true

		// No duplicate (project, remote) pairs
		key := proj + "|" + f.Remote
		if seenRemote[key] {
			return fmt.Errorf("duplicate (project, remote) pair: project=%q remote=%q", proj, f.Remote)
		}
		seenRemote[key] = true
	}

	seenPage := make(map[string]bool) // key: "project|page"
	for i, w := range m.Wikis {
		proj := w.ResolveProject(m.Project.ID)
		if proj == "" {
			return fmt.Errorf("wikis[%d] (%q): no project GUID — set [project].id or per-entry project field",
				i, w.Local)
		}

		if err := validateWikiPageName(w.Page); err != nil {
			return fmt.Errorf("wikis[%d] (%q): %w", i, w.Local, err)
		}

		// No duplicate local paths, across files and wikis alike.
		if seenLocal[w.Local] {
			return fmt.Errorf("duplicate local path %q in manifest", w.Local)
		}
		seenLocal[w.Local] = true

		key := proj + "|" + w.Page
		if seenPage[key] {
			return fmt.Errorf("duplicate (project, page) pair: project=%q page=%q", proj, w.Page)
		}
		seenPage[key] = true
	}

	seenSlug := make(map[string]bool)
	for i, d := range m.Datasets {
		if d.Slug == "" {
			return fmt.Errorf("datasets[%d]: slug cannot be blank", i)
		}
		if seenSlug[d.Slug] {
			return fmt.Errorf("duplicate dataset slug %q", d.Slug)
		}
		seenSlug[d.Slug] = true

		seenKey := make(map[string]bool)
		for j, f := range d.Files {
			if f.Local == "" {
				return fmt.Errorf("datasets[%q].files[%d]: local path cannot be blank", d.Slug, j)
			}
			// No duplicate local paths, across every section.
			if seenLocal[f.Local] {
				return fmt.Errorf("duplicate local path %q in manifest", f.Local)
			}
			seenLocal[f.Local] = true
			if seenKey[f.Key] {
				return fmt.Errorf("dataset %q: duplicate file key %q", d.Slug, f.Key)
			}
			seenKey[f.Key] = true
		}
	}

	seenPageSlug := make(map[string]bool)
	for i, p := range m.Site.Pages {
		if p.Local == "" {
			return fmt.Errorf("site.pages[%d]: local path cannot be blank", i)
		}
		if seenLocal[p.Local] {
			return fmt.Errorf("duplicate local path %q in manifest", p.Local)
		}
		seenLocal[p.Local] = true
		if seenPageSlug[p.Slug] {
			return fmt.Errorf("duplicate site page slug %q", p.Slug)
		}
		seenPageSlug[p.Slug] = true
	}
	return nil
}

// validateWikiPageName enforces OSF's wiki page name rules: non-blank, no
// forward slashes, at most 100 characters.
func validateWikiPageName(page string) error {
	if page == "" {
		return fmt.Errorf("wiki page name cannot be blank")
	}
	if strings.Contains(page, "/") {
		return fmt.Errorf("wiki page name %q cannot contain forward slashes", page)
	}
	if len(page) > 100 {
		return fmt.Errorf("wiki page name cannot be longer than 100 characters")
	}
	return nil
}
