package cmd

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/manifest"
)

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Lab Notes", "lab-notes"},
		{"RNA-seq (mouse)", "rna-seq-mouse"},
		{"  Spaced  Out  ", "spaced-out"},
		{"data", "data"},
		{"ALL CAPS", "all-caps"},
		{"---", ""},
		{"", ""},
		{"Ünïcode Títle", "ünïcode-títle"},
	}
	for _, tt := range tests {
		if got := slugify(tt.in); got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseMigrateSource(t *testing.T) {
	if got, err := parseMigrateSource("abc12"); err != nil || got != "abc12" {
		t.Errorf("parseMigrateSource(abc12) = %q, %v", got, err)
	}
	// Component addressing: the component GUID is itself a node.
	if got, err := parseMigrateSource("abc12/xyz34"); err != nil || got != "xyz34" {
		t.Errorf("parseMigrateSource(abc12/xyz34) = %q, %v", got, err)
	}
	if _, err := parseMigrateSource("abc12:/data"); err == nil {
		t.Error("a path-carrying target must be rejected — migrate exports whole nodes")
	}
	if _, err := parseMigrateSource(""); err == nil {
		t.Error("empty source must be rejected")
	}
}

func TestCreatorName(t *testing.T) {
	tests := []struct {
		full, given, family string
		want                string
	}{
		{"Ada Lovelace", "Ada", "Lovelace", "Lovelace, Ada"},
		{"Ada Lovelace", "", "", "Lovelace, Ada"},           // best-effort split on last space
		{"Ada King Lovelace", "", "", "Lovelace, Ada King"}, // multi-part given name
		{"Cher", "", "", "Cher"},                            // mononym stays as-is
		{"", "Grace", "Hopper", "Hopper, Grace"},            // no full name, structured parts
		{"", "", "", ""},                                    // nothing known
		{"  Padded Name  ", "", "", "Name, Padded"},         // whitespace tolerated
		{"Erin Smith-Jones", "Erin", "Smith-Jones", "Smith-Jones, Erin"},
	}
	for _, tt := range tests {
		if got := creatorName(tt.full, tt.given, tt.family); got != tt.want {
			t.Errorf("creatorName(%q,%q,%q) = %q, want %q", tt.full, tt.given, tt.family, got, tt.want)
		}
	}
}

func contribWith(full, given, family string, biblio bool) client.Contributor {
	var c client.Contributor
	c.Attributes.Bibliographic = biblio
	c.Embeds.Users.Data.Attributes.FullName = full
	c.Embeds.Users.Data.Attributes.GivenName = given
	c.Embeds.Users.Data.Attributes.FamilyName = family
	return c
}

func TestMigrateCreators(t *testing.T) {
	contribs := []client.Contributor{
		contribWith("Ada Lovelace", "Ada", "Lovelace", true),
		contribWith("Non Biblio", "Non", "Biblio", false), // non-bibliographic contributors are not authors
		contribWith("Grace Hopper", "", "", true),
		contribWith("", "", "", true), // nameless contributor is dropped
	}
	got := migrateCreators(contribs)
	want := []manifest.DatasetCreator{
		{Name: "Lovelace, Ada"},
		{Name: "Hopper, Grace"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("migrateCreators = %+v, want %+v", got, want)
	}
}

func TestMigrateMetadataSkeleton(t *testing.T) {
	creators := []manifest.DatasetCreator{{Name: "Lovelace, Ada"}}
	md := migrateMetadataSkeleton("My Project", "About it", []string{"rna", "mouse"}, creators, "abc12")

	if md.Title != "My Project" {
		t.Errorf("Title = %q", md.Title)
	}
	if md.Description != "About it" {
		t.Errorf("Description = %q", md.Description)
	}
	if !reflect.DeepEqual(md.Keywords, []string{"rna", "mouse"}) {
		t.Errorf("Keywords = %v", md.Keywords)
	}
	if !reflect.DeepEqual(md.Creators, creators) {
		t.Errorf("Creators = %+v", md.Creators)
	}
	if md.ResourceType != "dataset" {
		t.Errorf("ResourceType = %q, want dataset", md.ResourceType)
	}
	// OSF cannot supply these; the skeleton carries loud TODO markers that
	// `datapin check` / publishPreflight refuse, so nothing publishes by accident.
	if md.License != todoMarker {
		t.Errorf("License = %q, want %q", md.License, todoMarker)
	}
	if md.ContactEmail != todoMarker {
		t.Errorf("ContactEmail = %q, want %q", md.ContactEmail, todoMarker)
	}
	// Provenance survives into the published record.
	found := false
	for _, r := range md.Related {
		if r.Identifier == "https://osf.io/abc12" && r.Relation == "IsDerivedFrom" {
			found = true
		}
	}
	if !found {
		t.Errorf("Related = %+v, want IsDerivedFrom https://osf.io/abc12", md.Related)
	}
	// A blank title must not stay blank silently — it becomes a TODO marker.
	if md2 := migrateMetadataSkeleton("", "", nil, nil, "abc12"); md2.Title != todoMarker {
		t.Errorf("blank title should become the TODO marker, got %q", md2.Title)
	}
}

func TestMetadataTODOs(t *testing.T) {
	skeleton := migrateMetadataSkeleton("T", "", nil, []manifest.DatasetCreator{{Name: "Lovelace, Ada"}}, "abc12")
	todos := metadataTODOs(skeleton)
	for _, want := range []string{"license", "contact_email", "creator ORCIDs"} {
		if !containsString(todos, want) {
			t.Errorf("todos = %v, want to include %q", todos, want)
		}
	}

	complete := manifest.DatasetMetadata{
		Title:        "T",
		License:      "CC0-1.0",
		ContactEmail: "a@b.org",
		Creators:     []manifest.DatasetCreator{{Name: "Lovelace, Ada", ORCID: "0000-0002-1825-0097"}},
	}
	if got := metadataTODOs(complete); len(got) != 0 {
		t.Errorf("complete metadata should have no todos, got %v", got)
	}

	// No creators at all is a todo of its own.
	if got := metadataTODOs(manifest.DatasetMetadata{Title: "T", License: "CC0-1.0"}); !containsString(got, "creators") {
		t.Errorf("todos = %v, want creators", got)
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestMigrateEntryAction(t *testing.T) {
	tests := []struct {
		state       manifest.FileState
		localExists bool
		want        migAction
	}{
		{manifest.StateInSync, true, migKeep},
		{manifest.StatePinOnly, true, migKeep},
		// AHEAD: local is the going-forward truth; migration keeps it.
		{manifest.StateAheadOfManifest, true, migKeep},
		{manifest.StateMissing, false, migFetch},
		{manifest.StateBehind, true, migFetch},
		{manifest.StateRemoteNewer, true, migFetch},
		{manifest.StateNotPushed, true, migKeep},
		{manifest.StateNotPushed, false, migDrop},
		{manifest.StateDivergent, true, migFail},
	}
	for _, tt := range tests {
		if got := migrateEntryAction(tt.state, tt.localExists); got != tt.want {
			t.Errorf("migrateEntryAction(%s, local=%v) = %v, want %v", tt.state, tt.localExists, got, tt.want)
		}
	}
}

func TestParseDatasetOverrides(t *testing.T) {
	got, err := parseDatasetOverrides([]string{"raw=data/raw/**", "docs=*.md"})
	if err != nil {
		t.Fatalf("parseDatasetOverrides: %v", err)
	}
	want := []datasetOverride{{Slug: "raw", Pattern: "data/raw/**"}, {Slug: "docs", Pattern: "*.md"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	for _, bad := range []string{"noequals", "=pattern", "slug="} {
		if _, err := parseDatasetOverrides([]string{bad}); err == nil {
			t.Errorf("parseDatasetOverrides(%q) should error", bad)
		}
	}
}

func TestOverrideMatches(t *testing.T) {
	tests := []struct {
		pattern, local string
		want           bool
	}{
		{"data/raw/**", "data/raw/a.h5", true},
		{"data/raw/**", "data/raw/deep/b.h5", true},
		{"data/raw/**", "data/other.csv", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/x.md", false}, // path.Match: * does not cross /
		{"data/*.csv", "data/b.csv", true},
		{"data", "data/b.csv", true}, // bare directory name matches its contents
		{"data", "database.csv", false},
		{"README.md", "README.md", true},
	}
	for _, tt := range tests {
		if got := overrideMatches(tt.pattern, tt.local); got != tt.want {
			t.Errorf("overrideMatches(%q, %q) = %v, want %v", tt.pattern, tt.local, got, tt.want)
		}
	}
}

func TestGroupFiles_DefaultTopLevelDir(t *testing.T) {
	locals := []string{
		"results/x.txt",
		"data/raw/a.h5",
		"README.md",
		"data/b.csv",
	}
	got := groupFiles(locals, nil)
	want := []datasetGroup{
		{Slug: "data", Locals: []string{"data/b.csv", "data/raw/a.h5"}},
		{Slug: "results", Locals: []string{"results/x.txt"}},
		{Slug: "root", Locals: []string{"README.md"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groupFiles = %+v, want %+v", got, want)
	}
}

func TestGroupFiles_Overrides(t *testing.T) {
	locals := []string{"data/raw/a.h5", "data/b.csv", "README.md"}
	overrides := []datasetOverride{{Slug: "raw", Pattern: "data/raw/**"}}
	got := groupFiles(locals, overrides)
	want := []datasetGroup{
		{Slug: "raw", Locals: []string{"data/raw/a.h5"}},
		{Slug: "data", Locals: []string{"data/b.csv"}},
		{Slug: "root", Locals: []string{"README.md"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groupFiles = %+v, want %+v", got, want)
	}
}

func TestDedupeSlug(t *testing.T) {
	taken := map[string]bool{"data": true, "data-2": true}
	if got := dedupeSlug("data", taken); got != "data-3" {
		t.Errorf("dedupeSlug = %q, want data-3", got)
	}
	if got := dedupeSlug("fresh", taken); got != "fresh" {
		t.Errorf("dedupeSlug = %q, want fresh", got)
	}
	if got := dedupeSlug("", taken); got != "dataset" {
		t.Errorf("dedupeSlug(\"\") = %q, want dataset fallback", got)
	}
}

func TestWikiDocPath(t *testing.T) {
	if got := wikiDocPath("home"); got != "docs/home.md" {
		t.Errorf("wikiDocPath(home) = %q", got)
	}
	if got := wikiDocPath("Lab Notes"); got != "docs/lab-notes.md" {
		t.Errorf("wikiDocPath(Lab Notes) = %q", got)
	}
}

func TestRenderMigratedMD(t *testing.T) {
	p := migrateProvenance{
		GUIDs:             []string{"abc12"},
		Timestamp:         time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
		FileCount:         3,
		TotalBytes:        1234,
		WikiPages:         []string{"home", "Lab Notes"},
		Datasets:          []string{"data", "results"},
		ComponentsSkipped: []string{"xyz34"},
		TODOs:             []string{"license", "creator ORCIDs"},
	}
	md := renderMigratedMD(p)

	for _, want := range []string{
		"https://osf.io/abc12",
		"2026-08-11T12:00:00Z",
		"3 file(s)",
		"1234 bytes",
		"home",
		"Lab Notes",
		"data",
		"results",
		"xyz34",
		"license",
		"creator ORCIDs",
		"datapin publish", // DOI-mapping breadcrumb points at the next step
	} {
		if !strings.Contains(md, want) {
			t.Errorf("MIGRATED.md missing %q:\n%s", want, md)
		}
	}
}
