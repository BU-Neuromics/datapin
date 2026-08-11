package cmd

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// todoMarker is the loud placeholder migrate writes for metadata OSF cannot
// supply (license, contact email, a missing title). It is deliberately not a
// valid value anywhere it appears — `datapin check` flags it and
// publishPreflight refuses it — so a migrated skeleton can never publish by
// accident.
const todoMarker = "TODO"

// migAction is migrate's per-entry policy for manifest mode.
type migAction int

const (
	migKeep  migAction = iota // local copy is complete; carry it into a dataset
	migFetch                  // download from OSF so local is complete
	migDrop                   // nothing local and nothing remote; drop with a warning
	migFail                   // diverged; refuse before any transfer
)

// slugify renders s as a lowercase hyphen-separated slug: letters and digits
// are kept, every other run of characters collapses to a single hyphen.
func slugify(s string) string {
	var b strings.Builder
	lastHyphen := true // suppress a leading hyphen
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// parseMigrateSource extracts the node GUID to export from a migrate source
// argument. Component addressing (abc12/xyz34) resolves to the component GUID
// itself — OSF components are directly addressable nodes.
func parseMigrateSource(s string) (string, error) {
	if strings.Contains(s, ":") {
		return "", fmt.Errorf("migrate exports a whole node, not a path — pass the bare GUID (got %q)", s)
	}
	parts := strings.Split(s, "/")
	guid := parts[len(parts)-1]
	if guid == "" {
		return "", fmt.Errorf("missing OSF GUID in %q", s)
	}
	return guid, nil
}

// creatorName renders a contributor's name in the manifest's "Family, Given"
// form. Structured given/family names win; otherwise the full name is split
// best-effort on its last space (a mononym stays as-is). The result is only a
// skeleton — the user reviews creators (and adds ORCIDs) before publishing.
func creatorName(full, given, family string) string {
	given, family = strings.TrimSpace(given), strings.TrimSpace(family)
	if family != "" && given != "" {
		return family + ", " + given
	}
	full = strings.TrimSpace(full)
	if full == "" {
		if family != "" {
			return family
		}
		return given
	}
	idx := strings.LastIndex(full, " ")
	if idx < 0 {
		return full
	}
	return full[idx+1:] + ", " + full[:idx]
}

// migrateCreators maps a node's bibliographic contributors to creator
// skeletons (names only — OSF does not reliably expose ORCIDs or
// affiliations; `datapin check` nudges the user to fill them in).
func migrateCreators(contribs []client.Contributor) []manifest.DatasetCreator {
	var out []manifest.DatasetCreator
	for _, c := range contribs {
		if !c.Attributes.Bibliographic {
			continue
		}
		u := c.Embeds.Users.Data.Attributes
		name := creatorName(u.FullName, u.GivenName, u.FamilyName)
		if name == "" {
			continue
		}
		out = append(out, manifest.DatasetCreator{Name: name})
	}
	return out
}

// migrateMetadataSkeleton maps OSF node metadata onto a dataset metadata
// block: title/description/tags carry over, contributors become creator
// skeletons, and everything OSF cannot supply is a loud TODO marker. The
// origin is pinned as a DataCite IsDerivedFrom related identifier so it
// survives into the published record.
func migrateMetadataSkeleton(title, description string, tags []string, creators []manifest.DatasetCreator, guid string) manifest.DatasetMetadata {
	if title == "" {
		title = todoMarker
	}
	return manifest.DatasetMetadata{
		Title:        title,
		Description:  description,
		Keywords:     tags,
		ResourceType: "dataset",
		License:      todoMarker,
		ContactEmail: todoMarker,
		Creators:     creators,
		Related: []manifest.RelatedID{
			{Identifier: "https://osf.io/" + guid, Relation: "IsDerivedFrom"},
		},
	}
}

// metadataTODOs lists what a metadata block still needs a human for. It flags
// the migrate TODO markers plus the fields `datapin check` would raise, so
// the migrate summary and MIGRATED.md tell the user exactly what to edit.
func metadataTODOs(md manifest.DatasetMetadata) []string {
	var todos []string
	if md.Title == "" || md.Title == todoMarker {
		todos = append(todos, "title")
	}
	if md.License == "" || md.License == todoMarker {
		todos = append(todos, "license")
	}
	if md.ContactEmail == todoMarker {
		todos = append(todos, "contact_email")
	}
	if len(md.Creators) == 0 {
		todos = append(todos, "creators")
	} else {
		for _, c := range md.Creators {
			if c.ORCID == "" {
				todos = append(todos, "creator ORCIDs")
				break
			}
		}
	}
	return todos
}

// migrateEntryAction is migrate's whole manifest-mode policy for one entry,
// keyed on the same L/B/R classification the sync gates use:
//
//	IN_SYNC / PIN_ONLY   keep — local already holds the content
//	AHEAD_OF_MANIFEST    keep — local is the going-forward truth; migration
//	                     publishes local state, so newer local work is fine
//	MISSING / BEHIND /   fetch — local must be complete (and current) before
//	REMOTE_NEWER         the manifest stops referencing OSF
//	NOT_PUSHED + local   keep
//	NOT_PUSHED, no local drop — there is nothing anywhere to migrate
//	DIVERGED             fail hard before any transfer (same gate as sync)
func migrateEntryAction(state manifest.FileState, localExists bool) migAction {
	switch state {
	case manifest.StateMissing, manifest.StateBehind, manifest.StateRemoteNewer:
		return migFetch
	case manifest.StateNotPushed:
		if localExists {
			return migKeep
		}
		return migDrop
	case manifest.StateDivergent:
		return migFail
	}
	return migKeep
}

// datasetOverride is one parsed --dataset <slug>=<glob> assignment.
type datasetOverride struct {
	Slug    string
	Pattern string
}

// parseDatasetOverrides parses repeated --dataset flags of the form
// <slug>=<glob>.
func parseDatasetOverrides(specs []string) ([]datasetOverride, error) {
	var out []datasetOverride
	for _, spec := range specs {
		slug, pattern, ok := strings.Cut(spec, "=")
		if !ok || slug == "" || pattern == "" {
			return nil, fmt.Errorf("--dataset wants <slug>=<glob>, got %q", spec)
		}
		out = append(out, datasetOverride{Slug: slug, Pattern: pattern})
	}
	return out, nil
}

// overrideMatches reports whether a --dataset glob claims a local path.
// Matching is on the slash-separated path with path.Match semantics, plus two
// conveniences: a trailing "/**" (or a bare directory name) claims everything
// under that prefix.
func overrideMatches(pattern, local string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "/**"); ok {
		return local == prefix || strings.HasPrefix(local, prefix+"/")
	}
	if ok, err := path.Match(pattern, local); err == nil && ok {
		return true
	}
	// A bare name with no wildcard claims the directory of that name.
	if !strings.ContainsAny(pattern, "*?[") {
		return strings.HasPrefix(local, pattern+"/")
	}
	return false
}

// groupFiles proposes the dataset grouping for a set of local paths:
// overrides claim files first (in flag order); everything else groups by
// top-level directory, with repo-root files under the "root" slug. Output is
// deterministic — overrides in flag order, then default groups sorted by
// slug, files sorted within each group.
func groupFiles(locals []string, overrides []datasetOverride) []datasetGroup {
	claimed := make(map[string]bool)
	byOverride := make(map[string][]string)
	for _, local := range locals {
		for _, o := range overrides {
			if overrideMatches(o.Pattern, local) {
				byOverride[o.Slug] = append(byOverride[o.Slug], local)
				claimed[local] = true
				break
			}
		}
	}

	byDir := make(map[string][]string)
	for _, local := range locals {
		if claimed[local] {
			continue
		}
		slug := "root"
		if dir, _, ok := strings.Cut(local, "/"); ok {
			slug = slugify(dir)
			if slug == "" {
				slug = "root"
			}
		}
		byDir[slug] = append(byDir[slug], local)
	}

	var out []datasetGroup
	for _, o := range overrides {
		files := byOverride[o.Slug]
		if len(files) == 0 {
			continue
		}
		sort.Strings(files)
		out = append(out, datasetGroup{Slug: o.Slug, Locals: files})
		delete(byOverride, o.Slug) // repeated slug in overrides: first wins
	}
	slugs := make([]string, 0, len(byDir))
	for slug := range byDir {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		files := byDir[slug]
		sort.Strings(files)
		out = append(out, datasetGroup{Slug: slug, Locals: files})
	}
	return out
}

// datasetGroup is one proposed dataset: a slug plus its member files.
type datasetGroup struct {
	Slug   string
	Locals []string
}

// dedupeSlug returns slug, made unique against taken by appending -2, -3, …
// and records the result as taken. An empty slug falls back to "dataset".
func dedupeSlug(slug string, taken map[string]bool) string {
	if slug == "" {
		slug = "dataset"
	}
	out := slug
	for n := 2; taken[out]; n++ {
		out = fmt.Sprintf("%s-%d", slug, n)
	}
	taken[out] = true
	return out
}

// wikiDocPath is where an exported wiki page lands: docs/<slug>.md.
func wikiDocPath(page string) string {
	slug := slugify(page)
	if slug == "" {
		slug = "page"
	}
	return "docs/" + slug + ".md"
}

// migrateProvenance carries everything MIGRATED.md records.
type migrateProvenance struct {
	GUIDs             []string
	Timestamp         time.Time
	FileCount         int
	TotalBytes        int64
	WikiPages         []string
	Datasets          []string
	ComponentsSkipped []string
	TODOs             []string
}

// renderMigratedMD renders the provenance breadcrumb written next to the
// migrated data. It records where the data came from and when, what still
// needs a human before publishing, and holds the slot for the GUID ↔ DOI
// mapping that only exists after `datapin publish`.
func renderMigratedMD(p migrateProvenance) string {
	var b strings.Builder
	b.WriteString("# Migrated from OSF\n\n")
	b.WriteString("This project was exported from the Open Science Framework by `datapin migrate`.\n\n")
	for _, g := range p.GUIDs {
		fmt.Fprintf(&b, "- **Source:** <https://osf.io/%s> (GUID `%s`)\n", g, g)
	}
	fmt.Fprintf(&b, "- **Exported:** %s\n", p.Timestamp.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- **Files:** %d file(s), %d bytes\n", p.FileCount, p.TotalBytes)
	if len(p.WikiPages) > 0 {
		fmt.Fprintf(&b, "- **Wiki pages:** %s\n", strings.Join(p.WikiPages, ", "))
	}
	if len(p.Datasets) > 0 {
		fmt.Fprintf(&b, "- **Datasets:** %s\n", strings.Join(p.Datasets, ", "))
	}
	if len(p.ComponentsSkipped) > 0 {
		b.WriteString("\n## Components not exported\n\n")
		b.WriteString("These OSF components exist but were not exported (re-run with --components):\n\n")
		for _, c := range p.ComponentsSkipped {
			fmt.Fprintf(&b, "- <https://osf.io/%s>\n", c)
		}
	}
	if len(p.TODOs) > 0 {
		b.WriteString("\n## Before publishing (TODO)\n\n")
		b.WriteString("OSF could not supply these — edit `.datapin/datapin.toml`, then run `datapin check`:\n\n")
		for _, todo := range p.TODOs {
			fmt.Fprintf(&b, "- [ ] %s\n", todo)
		}
	}
	b.WriteString("\n## GUID ↔ DOI mapping\n\n")
	b.WriteString("| OSF GUID | DOI |\n|---|---|\n")
	for _, g := range p.GUIDs {
		fmt.Fprintf(&b, "| %s | _(record here after `datapin publish`)_ |\n", g)
	}
	return b.String()
}
