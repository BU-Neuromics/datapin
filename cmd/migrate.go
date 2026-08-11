package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var (
	migrateComponents  bool
	migrateDatasetsRaw []string
	migrateDryRun      bool
	migrateYes         bool
)

var migrateCmd = &cobra.Command{
	Use:   "migrate [<osf-guid>] [dest]",
	Short: "Export an OSF project into a datapin project (OSF sunset exit ramp)",
	Long: `Export an OSF project into a datapin project.

OSF is sunsetting its projects service; migrate is the one-command path off
the platform. It downloads files (MD5-verified), exports wiki pages as
markdown site pages, maps node metadata to a dataset metadata skeleton, and
scaffolds [[datasets]] in .datapin/datapin.toml. What OSF cannot supply
(license, ORCIDs, contact email) is written as loud TODO markers — review the
manifest and run 'datapin check' before 'datapin publish'.

GUID mode (an OSF GUID argument):
  datapin migrate abc12 [dest]
Exports node abc12 into dest (default: the current directory): every file
under osfstorage (structure preserved), every wiki page to docs/<page>.md, a
fresh manifest with one dataset, and a MIGRATED.md provenance breadcrumb.
Components are skipped with a notice unless --components exports each one as
its own dataset under a subdirectory. Re-running is idempotent: files whose
MD5 already matches are skipped, and metadata you have edited is preserved.

Manifest mode (no GUID; run inside a repo whose manifest tracks OSF):
  datapin migrate
Completes local copies first (MISSING/BEHIND entries are fetched with the
same safety gates as 'datapin sync'; a DIVERGED entry fails the run before
any transfer), groups [[files]] into [[datasets]] (default: one dataset per
top-level directory; --dataset overrides), converts [[wikis]] to site pages,
and rewrites the manifest atomically with the OSF sections removed.

Both modes leave publishing to you: nothing is uploaded and no DOI is minted.

With --output=json the result is:
  {"mode","source","dest","manifest","downloaded":[{"path","size"}],
   "skipped":[{"path"}],"wiki_pages":[{"page","local"}],
   "datasets":[{"slug","files"}],"components_skipped":[...],
   "todos":[...],"dry_run"}
JSON mode never prompts; when metadata TODOs remain the exit code is 1 (the
migration itself succeeded — the code signals incompleteness, as 'datapin
status' does).`,
	Args:         cobra.RangeArgs(0, 2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		overrides, err := parseDatasetOverrides(migrateDatasetsRaw)
		if err != nil {
			return err
		}
		if len(args) > 0 {
			if len(overrides) > 0 {
				return fmt.Errorf("--dataset applies to manifest mode (no GUID argument) — GUID mode scaffolds one dataset per node")
			}
			guid, err := parseMigrateSource(args[0])
			if err != nil {
				return err
			}
			dest := "."
			if len(args) == 2 {
				dest = args[1]
			}
			return runMigrateGUID(cmd.Context(), guid, dest)
		}
		if migrateComponents {
			return fmt.Errorf("--components applies to GUID mode (datapin migrate <osf-guid>)")
		}
		return runMigrateManifest(cmd.Context(), overrides)
	},
}

// ---- GUID mode ----

// migrateExport accumulates one GUID-mode export across the root node and
// (with --components) its component tree.
type migrateExport struct {
	ctx        context.Context
	osf        *client.OSFClient
	wb         *client.WaterbutlerClient
	destAbs    string
	dryRun     bool
	result     *output.MigrateResult
	datasets   []manifest.Dataset
	pages      []manifest.SitePage
	guids      []string
	wikiNames  []string
	slugs      map[string]bool
	pageSlugs  map[string]bool
	totalBytes int64
}

func runMigrateGUID(ctx context.Context, guid, dest string) error {
	token := config.LoadToken(flagToken)
	osf := client.New(token)
	wb := client.NewWaterbutler(token)

	node, err := osf.GetNode(ctx, guid)
	if err != nil {
		return friendlyAuthError(err)
	}

	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(destAbs, ".datapin", "datapin.toml")

	// A previous migrate run's manifest is merged (user edits win); a manifest
	// still tracking OSF belongs to manifest mode instead.
	var existing *manifest.Manifest
	if _, statErr := os.Stat(manifestPath); statErr == nil {
		existing, err = manifest.Load(manifestPath)
		if err != nil {
			return fmt.Errorf("loading existing manifest: %w", err)
		}
		if len(existing.Files) > 0 || len(existing.Wikis) > 0 {
			return fmt.Errorf("%s already tracks OSF via [[files]]/[[wikis]] — run 'datapin migrate' (no GUID) inside that project to convert it in place", manifestPath)
		}
	}

	ex := &migrateExport{
		ctx: ctx, osf: osf, wb: wb, destAbs: destAbs, dryRun: migrateDryRun,
		result:    output.NewMigrateResult("guid", guid, migrateDryRun),
		slugs:     map[string]bool{},
		pageSlugs: map[string]bool{},
	}
	ex.result.Dest = dest
	ex.result.Manifest = filepath.ToSlash(filepath.Join(dest, ".datapin", "datapin.toml"))

	log.Infof("exporting https://osf.io/%s (%s) → %s", guid, node.Attributes.Title, dest)
	if err := ex.exportNode(guid, node.Attributes, ""); err != nil {
		return err
	}

	children, err := osf.GetChildren(ctx, guid)
	if err != nil {
		log.Warnf("could not list components of %s: %v", guid, err)
	}
	if migrateComponents {
		if err := ex.exportComponents(children, ""); err != nil {
			return err
		}
	} else {
		for _, child := range children {
			log.Warnf("component %q (%s) not exported — re-run with --components to export it as its own dataset", child.Attributes.Title, child.ID)
			ex.result.ComponentsSkipped = append(ex.result.ComponentsSkipped, child.ID)
		}
	}

	m := mergeMigratedManifest(existing, ex.datasets, ex.pages, node.Attributes.Title)
	todos := manifestTODOs(m.Datasets)
	ex.result.TODOs = todos

	if !migrateDryRun {
		if err := manifest.Save(m, manifestPath); err != nil {
			return fmt.Errorf("writing manifest: %w", err)
		}
		log.Infof("wrote %s", ex.result.Manifest)
		if err := ex.writeProvenance(); err != nil {
			return err
		}
	}
	return finishMigrate(ex.result)
}

// exportNode exports one node's files, wikis, and metadata under prefix
// (empty for the root node; the component's slug path otherwise).
func (ex *migrateExport) exportNode(nodeID string, attrs client.NodeAttributes, parentPrefix string) error {
	slug := dedupeSlug(slugify(attrs.Title), ex.slugs)
	prefix := ""
	if parentPrefix != "" || len(ex.guids) > 0 { // any non-root node lives under its slug
		prefix = path.Join(parentPrefix, slug)
	}
	ex.guids = append(ex.guids, nodeID)

	contribs, err := ex.osf.GetContributors(ex.ctx, nodeID)
	if err != nil {
		log.Warnf("could not fetch contributors of %s: %v — fill in creators by hand", nodeID, err)
	}
	meta := migrateMetadataSkeleton(attrs.Title, attrs.Description, attrs.Tags, migrateCreators(contribs), nodeID)

	items, err := ex.osf.ListFiles(ex.ctx, nodeID)
	if err != nil {
		return friendlyAuthError(err)
	}
	var files []manifest.DatasetFile
	if err := ex.walkFiles(items, prefix, &files); err != nil {
		return err
	}
	if len(files) > 0 {
		ex.datasets = append(ex.datasets, manifest.Dataset{Slug: slug, Metadata: meta, Files: files})
		ex.result.Datasets = append(ex.result.Datasets, output.MigrateDataset{Slug: slug, Files: len(files)})
	} else {
		log.Infof("node %s has no files — no dataset scaffolded for it", nodeID)
	}

	return ex.exportWikis(nodeID, prefix, slug)
}

// exportComponents exports each component (and, recursively, its components)
// as its own dataset under its slug directory.
func (ex *migrateExport) exportComponents(children []client.Node, parentPrefix string) error {
	for _, child := range children {
		if err := ex.exportNode(child.ID, child.Attributes, parentPrefix); err != nil {
			return err
		}
		grandchildren, err := ex.osf.GetChildren(ex.ctx, child.ID)
		if err != nil {
			log.Warnf("could not list components of %s: %v", child.ID, err)
			continue
		}
		// The child's own slug was the last one reserved for a dataset; its
		// directory prefix is where grandchildren nest.
		childPrefix := path.Join(parentPrefix, slugOfLastDataset(ex, child.ID))
		if err := ex.exportComponents(grandchildren, childPrefix); err != nil {
			return err
		}
	}
	return nil
}

// slugOfLastDataset finds the slug exportNode assigned to nodeID's dataset by
// matching the IsDerivedFrom identifier; falls back to the slugified guid.
func slugOfLastDataset(ex *migrateExport, nodeID string) string {
	want := "https://osf.io/" + nodeID
	for _, d := range ex.datasets {
		for _, r := range d.Metadata.Related {
			if r.Identifier == want && r.Relation == "IsDerivedFrom" {
				return d.Slug
			}
		}
	}
	return slugify(nodeID)
}

// walkFiles recursively downloads every file item, preserving the osfstorage
// tree under prefix, and appends the dataset file pins.
func (ex *migrateExport) walkFiles(items []client.FileItem, prefix string, files *[]manifest.DatasetFile) error {
	for _, item := range items {
		if item.Attributes.Kind == "folder" {
			children, err := ex.osf.ListFilesFromURL(ex.ctx, item.Relationships.Files.Links.Related.Href)
			if err != nil {
				return fmt.Errorf("listing %s: %w", item.Attributes.Name, err)
			}
			if err := ex.walkFiles(children, prefix, files); err != nil {
				return err
			}
			continue
		}
		rel := strings.TrimPrefix(item.Attributes.MaterializedPath, "/")
		if rel == "" {
			rel = item.Attributes.Name
		}
		localRel := path.Join(prefix, rel)
		if err := ex.fetchFile(item, localRel); err != nil {
			return err
		}
		*files = append(*files, manifest.DatasetFile{Local: localRel, Key: rel})
	}
	return nil
}

// fetchFile downloads one file to its local path, verifying the MD5 the
// listing reported. A local copy that already matches is skipped, which is
// what makes re-running migrate resumable.
func (ex *migrateExport) fetchFile(item client.FileItem, localRel string) error {
	localAbs := filepath.Join(ex.destAbs, filepath.FromSlash(localRel))
	size := item.Attributes.Size
	remoteMD5 := item.Attributes.Extra.Hashes.MD5

	if localFileMatches(localAbs, remoteMD5) {
		log.Infof("≡ %s (already local, MD5 verified)", localRel)
		ex.result.Skipped = append(ex.result.Skipped, output.TransferItem{Path: localRel, Size: size})
		ex.totalBytes += size
		return nil
	}
	if ex.dryRun {
		log.Infof("[dry-run] would download %s (%d bytes)", localRel, size)
		ex.result.Downloaded = append(ex.result.Downloaded, output.TransferItem{Path: localRel, Size: size})
		ex.totalBytes += size
		return nil
	}
	if item.Links.Download == "" {
		return fmt.Errorf("no download URL for %q", localRel)
	}
	if err := os.MkdirAll(filepath.Dir(localAbs), 0755); err != nil {
		return err
	}
	if err := ex.wb.Download(ex.ctx, item.Links.Download, localAbs, size, progressBarEnabled()); err != nil {
		return fmt.Errorf("downloading %s: %w", localRel, err)
	}
	if remoteMD5 != "" {
		got, err := computeLocalMD5(localAbs)
		if err != nil {
			return err
		}
		if got != remoteMD5 {
			os.Remove(localAbs)
			return fmt.Errorf("MD5 mismatch after downloading %s: expected %s, got %s", localRel, remoteMD5, got)
		}
	} else {
		log.Warnf("%s: the listing reported no MD5, downloaded unverified", localRel)
	}
	log.Infof("↓ %s", localRel)
	ex.result.Downloaded = append(ex.result.Downloaded, output.TransferItem{Path: localRel, Size: size})
	ex.totalBytes += size
	return nil
}

// exportWikis writes every wiki page of a node to <prefix>/docs/<page>.md and
// registers it as a site page.
func (ex *migrateExport) exportWikis(nodeID, prefix, nodeSlug string) error {
	pages, err := ex.osf.ListWikis(ex.ctx, nodeID)
	if err != nil {
		if client.IsWikiDisabled(err) {
			return nil
		}
		log.Warnf("could not list wikis of %s: %v", nodeID, err)
		return nil
	}
	for _, pg := range pages {
		name := pg.Attributes.Name
		docRel := path.Join(prefix, wikiDocPath(name))
		slug := slugify(name)
		if prefix != "" {
			slug = nodeSlug + "-" + slug
		}
		slug = dedupeSlug(slug, ex.pageSlugs)

		if !ex.dryRun {
			content, cerr := ex.osf.GetWikiContent(ex.ctx, pg.ID)
			if cerr != nil {
				return fmt.Errorf("fetching wiki %q: %w", name, cerr)
			}
			localAbs := filepath.Join(ex.destAbs, filepath.FromSlash(docRel))
			if existing, rerr := os.ReadFile(localAbs); rerr == nil && bytes.Equal(client.CanonicalizeWikiContent(existing), client.CanonicalizeWikiContent(content)) {
				log.Infof("≡ wiki %s (already exported)", docRel)
			} else {
				if err := os.MkdirAll(filepath.Dir(localAbs), 0755); err != nil {
					return err
				}
				if err := writeFileAtomic(localAbs, content); err != nil {
					return fmt.Errorf("writing %s: %w", docRel, err)
				}
				log.Infof("↓ wiki %q → %s", name, docRel)
			}
		} else {
			log.Infof("[dry-run] would export wiki %q → %s", name, docRel)
		}
		ex.pages = append(ex.pages, manifest.SitePage{Local: docRel, Slug: slug})
		ex.wikiNames = append(ex.wikiNames, name)
		ex.result.WikiPages = append(ex.result.WikiPages, output.MigrateWikiPage{Page: name, Local: docRel})
	}
	return nil
}

// writeProvenance writes MIGRATED.md next to the exported data.
func (ex *migrateExport) writeProvenance() error {
	var slugs []string
	for _, d := range ex.datasets {
		slugs = append(slugs, d.Slug)
	}
	prov := migrateProvenance{
		GUIDs:             ex.guids,
		Timestamp:         time.Now(),
		FileCount:         len(ex.result.Downloaded) + len(ex.result.Skipped),
		TotalBytes:        ex.totalBytes,
		WikiPages:         ex.wikiNames,
		Datasets:          slugs,
		ComponentsSkipped: ex.result.ComponentsSkipped,
		TODOs:             ex.result.TODOs,
	}
	p := filepath.Join(ex.destAbs, "MIGRATED.md")
	if err := writeFileAtomic(p, []byte(renderMigratedMD(prov))); err != nil {
		return fmt.Errorf("writing MIGRATED.md: %w", err)
	}
	log.Infof("wrote MIGRATED.md")
	return nil
}

// mergeMigratedManifest builds the manifest a GUID-mode run writes, carrying
// forward whatever a previous run's manifest already holds: per-slug metadata
// (the user may have filled in the TODOs), publish pins, remote assignments,
// site config, and any datasets or pages the user added themselves.
func mergeMigratedManifest(existing *manifest.Manifest, datasets []manifest.Dataset, pages []manifest.SitePage, rootTitle string) *manifest.Manifest {
	m := &manifest.Manifest{}
	if existing != nil {
		m.Project = existing.Project
		m.Site = existing.Site
	}
	ourSlugs := map[string]bool{}
	for i := range datasets {
		d := &datasets[i]
		ourSlugs[d.Slug] = true
		if existing == nil {
			continue
		}
		if old := existing.FindDataset(d.Slug); old != nil {
			d.Archive, d.Workspace = old.Archive, old.Workspace
			d.Record, d.Concept = old.Record, old.Concept
			d.ConceptDOI, d.VersionDOI, d.Version = old.ConceptDOI, old.VersionDOI, old.Version
			d.Metadata = old.Metadata // the user's reviewed metadata wins over a fresh skeleton
		}
	}
	m.Datasets = datasets
	if existing != nil {
		for _, old := range existing.Datasets {
			if !ourSlugs[old.Slug] {
				m.Datasets = append(m.Datasets, old)
			}
		}
	}

	ourPageLocals := map[string]bool{}
	for _, p := range pages {
		ourPageLocals[p.Local] = true
	}
	merged := pages
	if existing != nil {
		for _, p := range existing.Site.Pages {
			if !ourPageLocals[p.Local] {
				merged = append(merged, p)
			}
		}
	}
	m.Site.Pages = merged
	if m.Site.Title == "" {
		m.Site.Title = rootTitle
	}
	return m
}

// manifestTODOs unions the metadata TODOs across datasets, deduplicated in
// first-seen order.
func manifestTODOs(datasets []manifest.Dataset) []string {
	seen := map[string]bool{}
	todos := []string{}
	for _, d := range datasets {
		for _, todo := range metadataTODOs(d.Metadata) {
			if !seen[todo] {
				seen[todo] = true
				todos = append(todos, todo)
			}
		}
	}
	return todos
}

// finishMigrate emits the result (JSON on stdout, or a human summary on
// stderr) and translates remaining metadata TODOs into the JSON-mode
// incompleteness exit code.
func finishMigrate(result *output.MigrateResult) error {
	if flagOutput == "json" {
		if err := output.PrintJSON(os.Stdout, result); err != nil {
			return err
		}
		if !result.DryRun && len(result.TODOs) > 0 {
			return &exitCodeError{code: 1}
		}
		return nil
	}
	prefix := ""
	if result.DryRun {
		prefix = "[dry-run] "
	}
	log.Infof("%smigrate: %d file(s) fetched, %d already local, %d wiki page(s), %d dataset(s)",
		prefix, len(result.Downloaded), len(result.Skipped), len(result.WikiPages), len(result.Datasets))
	if len(result.TODOs) > 0 {
		log.Warnf("metadata TODOs before publishing: %s", strings.Join(result.TODOs, ", "))
	}
	if !result.DryRun {
		log.Infof("next: review %s (fill in the TODO markers), then 'datapin check', 'datapin remote add', 'datapin publish'", result.Manifest)
	}
	return nil
}

// ---- Manifest mode ----

func runMigrateManifest(ctx context.Context, overrides []datasetOverride) error {
	jsonMode := flagOutput == "json"

	manifestPath, repoRoot, err := manifest.FindManifest()
	if manifest.IsNotFound(err) {
		return fmt.Errorf("nothing to migrate here — pass an OSF GUID to export a project (datapin migrate <guid> [dest]), or run inside a repo whose .datapin/datapin.toml tracks OSF files")
	}
	if err != nil {
		return err
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}
	if len(m.Files) == 0 && len(m.Wikis) == 0 {
		return fmt.Errorf("no [[files]] or [[wikis]] entries in %s — nothing references OSF (already migrated?)", manifestPath)
	}
	if m.Project.ID == "" {
		return fmt.Errorf("no project configured — run: datapin init <project-id>")
	}
	source := m.Project.ID

	token := config.LoadToken(flagToken)
	osf := client.New(token)
	wb := client.NewWaterbutler(token)
	res := resolver.New(resolver.NewCachingLister(osf))
	warnUnauthenticated(token, len(m.Files))

	// Pass 1: classify everything with the shared scan machinery.
	plans, err := scanEntries(ctx, m, repoRoot, res, osf, defaultScanJobs, false)
	if err != nil {
		return friendlyAPIError(err, token != "")
	}
	wikiPlans, err := scanWikiEntries(ctx, m, repoRoot, osf, false)
	if err != nil {
		return friendlyAPIError(err, token != "")
	}

	// Pre-flight: a diverged entry blocks the whole run before any transfer,
	// exactly like sync (resolve it there first).
	actions := make([]migAction, len(plans))
	wikiActions := make([]migAction, len(wikiPlans))
	var blocked []string
	for i, p := range plans {
		actions[i] = migrateEntryAction(p.state, p.localMD5 != "")
		if actions[i] == migFail {
			blocked = append(blocked, divergenceError(*p.entry, p.proj, p.localMD5, p.remoteVersions).Error())
		}
	}
	for i, p := range wikiPlans {
		wikiActions[i] = migrateEntryAction(p.state, p.localMD5 != "")
		if wikiActions[i] == migFail {
			blocked = append(blocked, wikiDivergenceError(*p.entry, p.proj, p.localMD5, p.remoteVersions).Error())
		}
	}
	if len(blocked) > 0 {
		return fmt.Errorf("%s", strings.Join(blocked, "\n\n"))
	}

	// The proposed grouping, over every entry that will exist locally.
	var locals []string
	projOf := map[string]string{}
	for i, p := range plans {
		switch actions[i] {
		case migKeep, migFetch:
			locals = append(locals, p.entry.Local)
			projOf[p.entry.Local] = p.proj
		case migDrop:
			log.Warnf("dropping %s — nothing local and nothing on the remote", p.entry.Local)
		}
	}
	groups := groupFiles(locals, overrides)

	// Confirmation on an interactive terminal (non-interactive and JSON runs
	// proceed — the rewrite is atomic and the old manifest is one git checkout
	// away).
	if !migrateYes && !migrateDryRun && !jsonMode && isInteractive() {
		printMigratePlan(groups, wikiPlans, wikiActions)
		if !confirm("Rewrite the manifest (removing [[files]]/[[wikis]]) and proceed?") {
			log.Warnf("aborted")
			return nil
		}
	}

	// Pass 2: fetch what local is missing, via the shared executor.
	result := output.NewMigrateResult("manifest", source, migrateDryRun)
	rel, relErr := filepath.Rel(".", manifestPath)
	if relErr != nil {
		rel = manifestPath
	}
	result.Manifest = filepath.ToSlash(rel)
	deps := transferDeps{res: res, wb: wb, osf: osf, showBar: progressBarEnabled(), dryRun: migrateDryRun}
	for i, p := range plans {
		switch actions[i] {
		case migFetch:
			if _, _, err := executeEntry(ctx, p, actionPull, deps); err != nil {
				return err
			}
			var size int64
			if info, serr := os.Stat(p.localAbs); serr == nil {
				size = info.Size()
			}
			result.Downloaded = append(result.Downloaded, output.TransferItem{Path: p.entry.Local, Size: size})
		case migKeep:
			result.Skipped = append(result.Skipped, output.TransferItem{Path: p.entry.Local})
		}
	}
	for i, p := range wikiPlans {
		switch wikiActions[i] {
		case migFetch:
			if _, _, err := executeWikiEntry(ctx, osf, p, actionPull, migrateDryRun); err != nil {
				return err
			}
		case migDrop:
			log.Warnf("dropping wiki %s — nothing local and nothing on the remote", p.entry.Local)
			continue
		}
		result.WikiPages = append(result.WikiPages, output.MigrateWikiPage{Page: p.entry.Page, Local: p.entry.Local})
	}

	// A fetch can come back unresolved (the remote path vanished); a dataset
	// must not reference a file that does not exist.
	if !migrateDryRun {
		groups = filterExistingGroups(groups, repoRoot)
	}

	// Node metadata → per-dataset skeletons.
	title, description := "", ""
	var tags []string
	if node, nerr := osf.GetNode(ctx, source); nerr != nil {
		log.Warnf("could not fetch node %s metadata: %v — titles left as TODO", source, nerr)
	} else {
		title, description, tags = node.Attributes.Title, node.Attributes.Description, node.Attributes.Tags
	}
	var creators []manifest.DatasetCreator
	if contribs, cerr := osf.GetContributors(ctx, source); cerr != nil {
		log.Warnf("could not fetch contributors of %s: %v — fill in creators by hand", source, cerr)
	} else {
		creators = migrateCreators(contribs)
	}

	taken := map[string]bool{}
	for _, d := range m.Datasets {
		taken[d.Slug] = true
	}
	multi := len(groups) > 1
	for _, g := range groups {
		slug := dedupeSlug(g.Slug, taken)
		dsTitle := title
		if multi && title != "" {
			dsTitle = title + " — " + slug
		}
		meta := migrateMetadataSkeleton(dsTitle, description, tags, creators, source)
		meta.Related = append(meta.Related, extraProjectRelations(g.Locals, projOf, source)...)
		files := make([]manifest.DatasetFile, 0, len(g.Locals))
		for _, local := range g.Locals {
			files = append(files, manifest.DatasetFile{Local: local, Key: local})
		}
		m.Datasets = append(m.Datasets, manifest.Dataset{Slug: slug, Metadata: meta, Files: files})
		result.Datasets = append(result.Datasets, output.MigrateDataset{Slug: slug, Files: len(files)})
	}

	// Wikis become site pages where their local files already live.
	pageLocals := map[string]bool{}
	pageSlugs := map[string]bool{}
	for _, p := range m.Site.Pages {
		pageLocals[p.Local] = true
		pageSlugs[p.Slug] = true
	}
	for _, wp := range result.WikiPages {
		if pageLocals[wp.Local] {
			continue
		}
		if !migrateDryRun {
			if _, serr := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(wp.Local))); serr != nil {
				log.Warnf("wiki page %q has no local file at %s — not added to the site", wp.Page, wp.Local)
				continue
			}
		}
		m.Site.Pages = append(m.Site.Pages, manifest.SitePage{Local: wp.Local, Slug: dedupeSlug(slugify(wp.Page), pageSlugs)})
		pageLocals[wp.Local] = true
	}
	if m.Site.Title == "" {
		m.Site.Title = title
	}

	// The manifest stops referencing OSF.
	m.Files = nil
	m.Wikis = nil
	m.Project.ID = ""

	result.TODOs = manifestTODOs(m.Datasets)

	if !migrateDryRun {
		if err := manifest.Save(m, manifestPath); err != nil {
			return fmt.Errorf("saving manifest: %w", err)
		}
		log.Infof("rewrote %s — [[files]]/[[wikis]] removed, content now owned by datasets and the site", result.Manifest)

		var wikiNames []string
		for _, wp := range result.WikiPages {
			wikiNames = append(wikiNames, wp.Page)
		}
		var slugs []string
		var totalBytes int64
		fileCount := 0
		for _, d := range m.Datasets {
			slugs = append(slugs, d.Slug)
			fileCount += len(d.Files)
			for _, f := range d.Files {
				if info, serr := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(f.Local))); serr == nil {
					totalBytes += info.Size()
				}
			}
		}
		prov := migrateProvenance{
			GUIDs: []string{source}, Timestamp: time.Now(),
			FileCount: fileCount, TotalBytes: totalBytes,
			WikiPages: wikiNames, Datasets: slugs, TODOs: result.TODOs,
		}
		if err := writeFileAtomic(filepath.Join(repoRoot, "MIGRATED.md"), []byte(renderMigratedMD(prov))); err != nil {
			return fmt.Errorf("writing MIGRATED.md: %w", err)
		}
		log.Infof("wrote MIGRATED.md")
	}
	return finishMigrate(result)
}

// printMigratePlan logs the proposed grouping before the interactive confirm.
func printMigratePlan(groups []datasetGroup, wikiPlans []wikiEntryPlan, wikiActions []migAction) {
	for _, g := range groups {
		log.Infof("dataset %q: %d file(s)", g.Slug, len(g.Locals))
		for _, local := range g.Locals {
			log.Infof("  %s", local)
		}
	}
	for i, p := range wikiPlans {
		if wikiActions[i] != migDrop {
			log.Infof("site page: %s (wiki %q)", p.entry.Local, p.entry.Page)
		}
	}
}

// extraProjectRelations adds IsDerivedFrom links for entries that referenced
// a different OSF node than the manifest default (per-entry project
// overrides), so multi-node provenance is not flattened away.
func extraProjectRelations(locals []string, projOf map[string]string, defaultProj string) []manifest.RelatedID {
	seen := map[string]bool{defaultProj: true}
	var out []manifest.RelatedID
	for _, local := range locals {
		proj := projOf[local]
		if proj == "" || seen[proj] {
			continue
		}
		seen[proj] = true
		out = append(out, manifest.RelatedID{Identifier: "https://osf.io/" + proj, Relation: "IsDerivedFrom"})
	}
	return out
}

// filterExistingGroups drops files that still do not exist locally after the
// fetch pass (e.g. a MISSING entry whose remote path vanished), removing any
// group left empty.
func filterExistingGroups(groups []datasetGroup, repoRoot string) []datasetGroup {
	out := groups[:0]
	for _, g := range groups {
		kept := g.Locals[:0]
		for _, local := range g.Locals {
			if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(local))); err == nil {
				kept = append(kept, local)
			} else {
				log.Warnf("dropping %s — no local file after the fetch pass", local)
			}
		}
		g.Locals = kept
		if len(g.Locals) > 0 {
			out = append(out, g)
		}
	}
	return out
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateComponents, "components", false, "GUID mode: export each OSF component as its own dataset (default: root node only, with a notice)")
	migrateCmd.Flags().StringArrayVar(&migrateDatasetsRaw, "dataset", nil, "Manifest mode: assign files to a dataset as <slug>=<glob> (repeatable; default: one dataset per top-level directory)")
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "Print the full migration plan without downloading or writing anything")
	migrateCmd.Flags().BoolVar(&migrateYes, "yes", false, "Skip the interactive confirmation before rewriting the manifest")
	rootCmd.AddCommand(migrateCmd)
}
