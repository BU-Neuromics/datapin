package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/gitutil"
	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// ---- legacy OSF flow helpers (kept behind --osf) ----

func TestRemotePath(t *testing.T) {
	cases := []struct {
		base, rel, want string
	}{
		{"/", "data/x.csv", "/data/x.csv"},
		{"", "data/x.csv", "/data/x.csv"},
		{"/inputs", "data/x.csv", "/inputs/data/x.csv"},
		{"inputs/", "x.csv", "/inputs/x.csv"},
		{"/inputs/", "/x.csv", "/inputs/x.csv"},
	}
	for _, tc := range cases {
		if got := remotePath(tc.base, tc.rel); got != tc.want {
			t.Errorf("remotePath(%q,%q) = %q, want %q", tc.base, tc.rel, got, tc.want)
		}
	}
}

func TestUntrackedCandidates(t *testing.T) {
	m := &manifest.Manifest{Files: []manifest.Entry{
		{Local: "data/tracked.csv"},
		{Local: "notes.txt"},
	}}
	cands := []gitutil.Candidate{
		{Path: "data/tracked.csv"},
		{Path: "data/new.csv"},
		{Path: "notes.txt"},
		{Path: "fresh.bin"},
	}
	got := untrackedCandidates(cands, m)
	var paths []string
	for _, c := range got {
		paths = append(paths, c.Path)
	}
	if !reflect.DeepEqual(paths, []string{"data/new.csv", "fresh.bin"}) {
		t.Errorf("untrackedCandidates = %v, want [data/new.csv fresh.bin]", paths)
	}
}

// ---- publish-first flow helpers ----

func TestArchiveRemotes(t *testing.T) {
	remotes := []config.Remote{
		{Name: "sandbox", Kind: "invenio"},
		{Name: "scratch", Kind: "dir"},
		{Name: "bucket", Kind: "s3"},
		{Name: "dv", Kind: "dataverse"},
		{Name: "cluster", Kind: "sftp"},
		{Name: "fig", Kind: "figshare"},
	}
	got := archiveRemotes(remotes)
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	if !reflect.DeepEqual(names, []string{"sandbox", "dv", "fig"}) {
		t.Errorf("archiveRemotes = %v, want [sandbox dv fig]", names)
	}
}

// The sandbox rehearsal path is deliberately option 1 (issue #25: "sandbox-first
// rehearsal path front and center") and the default on an empty answer.
func TestOnboardRemoteOptions_SandboxFirst(t *testing.T) {
	opts := onboardRemoteOptions()
	if len(opts) < 5 {
		t.Fatalf("want at least 5 remote options, got %d", len(opts))
	}
	first := opts[0]
	if first.Kind != "invenio" || first.URL != "https://sandbox.zenodo.org" {
		t.Errorf("option 1 must be the Zenodo sandbox, got %+v", first)
	}
	kinds := map[string]bool{}
	for _, o := range opts {
		kinds[o.Kind] = true
	}
	for _, k := range []string{"invenio", "dataverse", "figshare"} {
		if !kinds[k] {
			t.Errorf("remote options missing kind %q", k)
		}
	}
}

func TestRemoteOptionFor(t *testing.T) {
	opts := onboardRemoteOptions()
	// Empty answer defaults to the sandbox (option 1).
	got, ok := remoteOptionFor("")
	if !ok || got != opts[0] {
		t.Errorf("remoteOptionFor(\"\") = %+v, %v; want the sandbox option", got, ok)
	}
	got, ok = remoteOptionFor("2")
	if !ok || got != opts[1] {
		t.Errorf("remoteOptionFor(2) = %+v, %v; want option 2", got, ok)
	}
	for _, bad := range []string{"0", "99", "x"} {
		if _, ok := remoteOptionFor(bad); ok {
			t.Errorf("remoteOptionFor(%q) should not be ok", bad)
		}
	}
}

func TestPickRemoteAnswer(t *testing.T) {
	cases := []struct {
		ans    string
		n      int
		idx    int
		addNew bool
		ok     bool
	}{
		{"", 3, 0, false, true}, // default: first remote
		{"1", 3, 0, false, true},
		{"3", 3, 2, false, true},
		{"n", 3, 0, true, true},
		{"N", 3, 0, true, true},
		{"4", 3, 0, false, false},
		{"0", 3, 0, false, false},
		{"zap", 3, 0, false, false},
	}
	for _, tc := range cases {
		idx, addNew, ok := pickRemoteAnswer(tc.ans, tc.n)
		if idx != tc.idx || addNew != tc.addNew || ok != tc.ok {
			t.Errorf("pickRemoteAnswer(%q,%d) = (%d,%v,%v), want (%d,%v,%v)",
				tc.ans, tc.n, idx, addNew, ok, tc.idx, tc.addNew, tc.ok)
		}
	}
}

// The license is an explicit choice (D37): CC0-1.0 suggested, CC-BY-4.0 the
// named alternative, any SPDX id typable, an explicit "decide later" — and no
// silent prefill: an unrecognized answer is not ok rather than defaulted.
func TestLicenseFromChoice(t *testing.T) {
	cases := []struct {
		ans     string
		license string
		needsID bool
		ok      bool
	}{
		{"1", "CC0-1.0", false, true},
		{"2", "CC-BY-4.0", false, true},
		{"3", "", true, true},
		{"4", "", false, true}, // decide later → empty license
		{"", "", false, false}, // no default — the choice must be explicit
		{"5", "", false, false},
		{"cc0", "", false, false},
	}
	for _, tc := range cases {
		license, needsID, ok := licenseFromChoice(tc.ans)
		if license != tc.license || needsID != tc.needsID || ok != tc.ok {
			t.Errorf("licenseFromChoice(%q) = (%q,%v,%v), want (%q,%v,%v)",
				tc.ans, license, needsID, ok, tc.license, tc.needsID, tc.ok)
		}
	}
}

func TestOnboardDatasetCandidates(t *testing.T) {
	m := &manifest.Manifest{
		Files: []manifest.Entry{{Local: "synced.csv"}},
		Wikis: []manifest.WikiEntry{{Local: "docs/home.md"}},
		Datasets: []manifest.Dataset{{
			Slug:  "counts",
			Files: []manifest.DatasetFile{{Local: "data/counts.h5"}},
		}},
	}
	cands := []gitutil.Candidate{
		{Path: "synced.csv"},
		{Path: "docs/home.md"},
		{Path: "data/counts.h5"},
		{Path: "data/new.csv"},
	}
	got := onboardDatasetCandidates(cands, m)
	var paths []string
	for _, c := range got {
		paths = append(paths, c.Path)
	}
	if !reflect.DeepEqual(paths, []string{"data/new.csv"}) {
		t.Errorf("onboardDatasetCandidates = %v, want [data/new.csv]", paths)
	}
}

func TestDefaultDatasetSlug(t *testing.T) {
	m := &manifest.Manifest{}
	if got := defaultDatasetSlug("/home/me/My Project", m); got != "my-project" {
		t.Errorf("defaultDatasetSlug = %q, want my-project", got)
	}
	// Taken slugs are deduped, never duplicated.
	m.Datasets = []manifest.Dataset{{Slug: "my-project"}}
	if got := defaultDatasetSlug("/home/me/My Project", m); got != "my-project-2" {
		t.Errorf("defaultDatasetSlug (taken) = %q, want my-project-2", got)
	}
}

// Dataset file keys are the full slash-separated local path (D49): flat
// basename keys collide across directories.
func TestDatasetFilesFor(t *testing.T) {
	got := datasetFilesFor([]string{"data/a/x.csv", "data/b/x.csv", "top.txt"})
	want := []manifest.DatasetFile{
		{Local: "data/a/x.csv", Key: "data/a/x.csv"},
		{Local: "data/b/x.csv", Key: "data/b/x.csv"},
		{Local: "top.txt", Key: "top.txt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("datasetFilesFor = %+v, want %+v", got, want)
	}
}

func TestContactEmailRequired(t *testing.T) {
	if !contactEmailRequired("dataverse") {
		t.Error("Dataverse requires contact_email (D38)")
	}
	for _, k := range []string{"invenio", "figshare", ""} {
		if contactEmailRequired(k) {
			t.Errorf("contactEmailRequired(%q) = true, want false", k)
		}
	}
}

// scriptedPrompter feeds canned answers to the collect* helpers.
func scriptedPrompter(t *testing.T, answers ...string) prompter {
	t.Helper()
	i := 0
	next := func() string {
		if i >= len(answers) {
			t.Fatalf("prompter exhausted after %d answers", len(answers))
		}
		a := answers[i]
		i++
		return a
	}
	return prompter{
		line: func(prompt, def string) string {
			if a := next(); a != "" {
				return a
			}
			return def
		},
		yes: func(prompt string) bool { return next() == "y" },
	}
}

func TestCollectTitle_LoopsUntilNonEmpty(t *testing.T) {
	p := scriptedPrompter(t, "", "", "RNA-seq counts")
	if got := collectTitle(p); got != "RNA-seq counts" {
		t.Errorf("collectTitle = %q", got)
	}
}

func TestCollectCreators(t *testing.T) {
	// First creator: empty name re-asked; invalid ORCID re-asked; then a
	// second creator with a URL-form ORCID that normalizes; then stop.
	p := scriptedPrompter(t,
		"", "Doe, Jane", // name: re-ask on empty
		"1234", "0000-0002-1825-0097", // orcid: invalid then valid
		"BU", // affiliation
		"y",  // add another
		"Ríos, Ana",
		"https://orcid.org/0000-0002-1825-0097", // URL form normalizes
		"",                                      // affiliation skipped
		"n",                                     // stop
	)
	got := collectCreators(p)
	want := []manifest.DatasetCreator{
		{Name: "Doe, Jane", ORCID: "0000-0002-1825-0097", Affiliation: "BU"},
		{Name: "Ríos, Ana", ORCID: "0000-0002-1825-0097"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collectCreators = %+v, want %+v", got, want)
	}
}

func TestCollectCreators_EmptyORCIDAllowed(t *testing.T) {
	p := scriptedPrompter(t, "Doe, Jane", "", "", "n")
	got := collectCreators(p)
	if len(got) != 1 || got[0].ORCID != "" {
		t.Errorf("collectCreators = %+v, want one creator without ORCID", got)
	}
}

func TestCollectLicense(t *testing.T) {
	// Unrecognized answers re-ask (no silent default), then CC0.
	p := scriptedPrompter(t, "", "9", "1")
	if got := collectLicense(p); got != "CC0-1.0" {
		t.Errorf("collectLicense = %q, want CC0-1.0", got)
	}
	// Custom SPDX id path.
	p = scriptedPrompter(t, "3", "", "ODbL-1.0")
	if got := collectLicense(p); got != "ODbL-1.0" {
		t.Errorf("collectLicense (custom) = %q, want ODbL-1.0", got)
	}
	// Decide later → empty.
	p = scriptedPrompter(t, "4")
	if got := collectLicense(p); got != "" {
		t.Errorf("collectLicense (later) = %q, want empty", got)
	}
}

func TestCollectContactEmail(t *testing.T) {
	// Dataverse target: required (D38) — empty answers re-ask.
	p := scriptedPrompter(t, "", "pi@bu.edu")
	if got := collectContactEmail(p, "dataverse"); got != "pi@bu.edu" {
		t.Errorf("collectContactEmail(dataverse) = %q", got)
	}
	// Other targets: optional.
	p = scriptedPrompter(t, "")
	if got := collectContactEmail(p, "invenio"); got != "" {
		t.Errorf("collectContactEmail(invenio) = %q, want empty", got)
	}
}

// The legacy OSF flags refuse to run against the publish flow: they only mean
// something under --osf.
func TestOnboardLegacyFlagGuard(t *testing.T) {
	if err := checkLegacyOnboardFlags(false, "abc12", ""); err == nil {
		t.Error("--project without --osf must error")
	}
	if err := checkLegacyOnboardFlags(false, "", "/base"); err == nil {
		t.Error("--remote-base without --osf must error")
	}
	if err := checkLegacyOnboardFlags(true, "abc12", "/base"); err != nil {
		t.Errorf("legacy flags under --osf should pass, got %v", err)
	}
	if err := checkLegacyOnboardFlags(false, "", ""); err != nil {
		t.Errorf("no legacy flags should pass, got %v", err)
	}
}

// The workspace track is offered, never assumed: the menu maps to the three
// workspace kinds, and an unrecognized answer is not ok (no silent default).
func TestWorkspaceKindFromChoice(t *testing.T) {
	cases := []struct {
		ans  string
		kind string
		ok   bool
	}{
		{"1", "dir", true},
		{"2", "s3", true},
		{"3", "sftp", true},
		{"", "", false},
		{"4", "", false},
		{"s3", "", false},
	}
	for _, tc := range cases {
		kind, ok := workspaceKindFromChoice(tc.ans)
		if kind != tc.kind || ok != tc.ok {
			t.Errorf("workspaceKindFromChoice(%q) = (%q,%v), want (%q,%v)", tc.ans, kind, ok, tc.kind, tc.ok)
		}
	}
	// Every offered kind must actually carry the workspace role (D33).
	for _, ans := range []string{"1", "2", "3"} {
		kind, _ := workspaceKindFromChoice(ans)
		if !isWorkspaceKind(kind) {
			t.Errorf("workspaceKindFromChoice(%q) = %q, which is not a workspace kind", ans, kind)
		}
	}
}

// Guard against accidentally weakening the deprecation posture: the --osf
// escape hatch help text must say it is deprecated.
func TestOnboardOSFFlagDeprecationNote(t *testing.T) {
	f := onboardCmd.Flags().Lookup("osf")
	if f == nil {
		t.Fatal("onboard must keep an --osf escape hatch")
	}
	if !strings.Contains(strings.ToLower(f.Usage), "deprecated") {
		t.Errorf("--osf help text must carry a deprecation note, got %q", f.Usage)
	}
}
