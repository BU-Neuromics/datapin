//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// setupDataset writes a manifest with one dataset and its files, plus the
// remote config pointing at the fake.
func (e *invenioEnv) setupDataset(t *testing.T) {
	t.Helper()
	if _, stderr, code := e.run("remote", "add", e.srv.URL(),
		"--name", "sandbox", "--token-value", invenioTestToken, "--no-keychain"); code != 0 {
		t.Fatalf("remote add: %s", stderr)
	}
	e.setupDatasetNoRemote(t, "")
}

// setupDatasetNoRemote writes the dataset fixture without touching the
// remote config (the remote may be added separately, or deliberately probed
// differently). extraMetadata is appended inside [datasets.metadata].
func (e *invenioEnv) setupDatasetNoRemote(t *testing.T, extraMetadata string) {
	t.Helper()
	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(e.dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("results/counts.csv", "gene,count\nA,1\n")
	mustWrite("results/meta.csv", "sample,batch\nS1,1\n")
	mustWrite(".datapin/datapin.toml", `
schema = 2

[project]
default_archive = "sandbox"

[[datasets]]
slug = "counts"

  [datasets.metadata]
  title = "integration test dataset"
  license = "CC0-1.0"
  `+extraMetadata+`
  [[datasets.metadata.creators]]
  name = "Tester, Trusty"

  [[datasets.files]]
  local = "results/counts.csv"
  [[datasets.files]]
  local = "results/meta.csv"
`)
}

type publishJSON struct {
	Slug       string `json:"slug"`
	State      string `json:"state"`
	Record     string `json:"record"`
	Version    int    `json:"version"`
	DOI        string `json:"doi"`
	ConceptDOI string `json:"concept_doi"`
	Files      []struct {
		Key    string `json:"key"`
		Action string `json:"action"`
	} `json:"files"`
	ReservedDOI string `json:"reserved_doi"`
	DryRun      bool   `json:"dry_run"`
}

func mustPublish(t *testing.T, e *invenioEnv, args ...string) []publishJSON {
	t.Helper()
	full := append([]string{"publish"}, args...)
	full = append(full, "--yes", "--output=json")
	stdout, stderr, code := e.run(full...)
	if code != 0 {
		t.Fatalf("publish failed (%d): %s\n%s", code, stdout, stderr)
	}
	var out []publishJSON
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("publish JSON: %v\n%s", err, stdout)
	}
	return out
}

func TestPublish_FirstAndNewVersion(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)

	// First publish mints concept + version DOIs and pins the manifest.
	res := mustPublish(t, e, "counts")
	if len(res) != 1 || res[0].State != "PUBLISHED" || res[0].Version != 1 {
		t.Fatalf("first publish = %+v", res)
	}
	if !strings.HasPrefix(res[0].DOI, "10.5072/zenodo.") || res[0].ConceptDOI == "" {
		t.Fatalf("DOIs = %q / %q", res[0].DOI, res[0].ConceptDOI)
	}
	manifestBytes, _ := os.ReadFile(filepath.Join(e.dir, ".datapin", "datapin.toml"))
	if !strings.Contains(string(manifestBytes), res[0].DOI) {
		t.Errorf("manifest not re-pinned with the version DOI:\n%s", manifestBytes)
	}

	// Republish with no changes: IN_SYNC no-op, no new version.
	res = mustPublish(t, e, "counts")
	if res[0].State != "IN_SYNC" || res[0].Version != 1 {
		t.Fatalf("idempotent republish = %+v", res)
	}

	// Change one file → version 2, with only the changed file uploaded.
	if err := os.WriteFile(filepath.Join(e.dir, "results/counts.csv"), []byte("gene,count\nA,2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res = mustPublish(t, e, "counts")
	if res[0].State != "PUBLISHED" || res[0].Version != 2 {
		t.Fatalf("v2 publish = %+v", res)
	}
	if res[0].DOI == res[0].ConceptDOI {
		t.Error("version DOI must differ from concept DOI")
	}
	actions := map[string]string{}
	for _, f := range res[0].Files {
		actions[f.Key] = f.Action
	}
	if actions["counts.csv"] != "replace" || actions["meta.csv"] != "keep" {
		t.Errorf("v2 actions = %v, want replace+keep", actions)
	}
}

func TestPublish_DryRunPublishesNothing(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)

	stdout, stderr, code := e.run("publish", "counts", "--dry-run", "--output=json")
	if code != 0 {
		t.Fatalf("dry-run failed (%d): %s", code, stderr)
	}
	var out []publishJSON
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("JSON: %v\n%s", err, stdout)
	}
	if out[0].State != "DRY_RUN" || !out[0].DryRun {
		t.Fatalf("dry-run = %+v", out[0])
	}
	if ids := e.srv.PublishedIDs(); len(ids) != 0 {
		t.Errorf("dry-run published records: %v", ids)
	}
}

func TestPublish_JSONRequiresYes(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	_, stderr, code := e.run("publish", "counts", "--output=json")
	if code == 0 || !strings.Contains(stderr, "--yes") {
		t.Fatalf("JSON publish without --yes: code=%d stderr=%s", code, stderr)
	}
}

// A missing license is only a warning for `check`, but publish must
// refuse: otherwise the backend applies its own default license and the
// user grants rights they never chose (D37).
func TestPublish_MissingLicenseRefused(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	p := filepath.Join(e.dir, ".datapin", "datapin.toml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	stripped := strings.ReplaceAll(string(data), "license = \"CC0-1.0\"", "")
	if err := os.WriteFile(p, []byte(stripped), 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := e.run("publish", "counts", "--yes")
	if code == 0 || !strings.Contains(stderr, "license") {
		t.Fatalf("publish without license: code=%d stderr=%s", code, stderr)
	}
}

func TestPublish_MissingMetadataRefused(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	// Strip the metadata block.
	p := filepath.Join(e.dir, ".datapin", "datapin.toml")
	data, _ := os.ReadFile(p)
	stripped := strings.ReplaceAll(string(data), "title = \"integration test dataset\"", "")
	if err := os.WriteFile(p, []byte(stripped), 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := e.run("publish", "counts", "--yes")
	if code == 0 || !strings.Contains(stderr, "title") {
		t.Fatalf("publish without title: code=%d stderr=%s", code, stderr)
	}
}

func TestPublish_Reserve(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)

	res := mustPublish(t, e, "counts", "--reserve")
	if res[0].State != "RESERVED" || !strings.HasPrefix(res[0].ReservedDOI, "10.5072/") {
		t.Fatalf("reserve = %+v", res[0])
	}
	if ids := e.srv.PublishedIDs(); len(ids) != 0 {
		t.Errorf("--reserve must not publish, got %v", ids)
	}
	// Manifest must stay unpinned.
	data, _ := os.ReadFile(filepath.Join(e.dir, ".datapin", "datapin.toml"))
	if strings.Contains(string(data), res[0].ReservedDOI) {
		t.Error("reserve must not pin the manifest")
	}
}

func TestDatasetVersionsAndPull(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	mustPublish(t, e, "counts")
	if err := os.WriteFile(filepath.Join(e.dir, "results/counts.csv"), []byte("gene,count\nA,3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, e, "counts")

	// versions <slug> lists the chain, newest first, with the pin marked.
	stdout, stderr, code := e.run("versions", "counts", "--output=json")
	if code != 0 {
		t.Fatalf("versions failed: %s", stderr)
	}
	var vres struct {
		Slug     string `json:"slug"`
		Versions []struct {
			Version int  `json:"version"`
			Latest  bool `json:"latest"`
			Pinned  bool `json:"pinned"`
		} `json:"versions"`
	}
	if err := json.Unmarshal([]byte(stdout), &vres); err != nil {
		t.Fatalf("versions JSON: %v\n%s", err, stdout)
	}
	if len(vres.Versions) != 2 || vres.Versions[0].Version != 2 || !vres.Versions[0].Latest || !vres.Versions[0].Pinned {
		t.Fatalf("versions = %+v", vres)
	}

	// Simulate the laptop side: delete local bytes, pull the pinned version.
	if err := os.Remove(filepath.Join(e.dir, "results/counts.csv")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = e.run("pull", "counts", "--output=json")
	if code != 0 {
		t.Fatalf("pull failed: %s\n%s", stdout, stderr)
	}
	got, _ := os.ReadFile(filepath.Join(e.dir, "results/counts.csv"))
	if string(got) != "gene,count\nA,3\n" {
		t.Errorf("pulled bytes = %q", got)
	}

	// Idempotent: second pull downloads nothing.
	stdout, _, _ = e.run("pull", "counts", "--output=json")
	if !strings.Contains(stdout, `"skipped"`) || strings.Contains(stdout, `"downloaded"`) {
		t.Errorf("second pull not idempotent: %s", stdout)
	}
}

func TestPublish_RefusesWhenRemoteNewer(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	mustPublish(t, e, "counts")

	// Rewind the manifest pin to simulate another clone having published.
	p := filepath.Join(e.dir, ".datapin", "datapin.toml")
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(data), "version = 1", "version = 0")), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "results/counts.csv"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := e.run("publish", "counts", "--yes")
	if code == 0 || !strings.Contains(stderr, "newer") == false && !strings.Contains(stderr, "version") {
		t.Fatalf("publish with stale pin: code=%d stderr=%s", code, stderr)
	}
	if code == 0 {
		t.Fatal("publish with a stale pin must be refused without --force")
	}
}

func TestStatus_DatasetRows(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)

	// Unpublished dataset: NOT_PUSHED-style row, exit 1.
	stdout, _, code := e.run("status", "--output=json")
	if code != 1 {
		t.Fatalf("status before publish: code=%d\n%s", code, stdout)
	}
	if !strings.Contains(stdout, `"kind": "dataset"`) && !strings.Contains(stdout, `"kind":"dataset"`) {
		t.Fatalf("no dataset row in status JSON:\n%s", stdout)
	}
	if !strings.Contains(stdout, "NOT_PUBLISHED") {
		t.Errorf("want NOT_PUBLISHED, got:\n%s", stdout)
	}

	mustPublish(t, e, "counts")
	stdout, _, code = e.run("status", "--output=json")
	if code != 0 {
		t.Fatalf("status after publish: code=%d\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "IN_SYNC") {
		t.Errorf("want IN_SYNC after publish, got:\n%s", stdout)
	}

	// Local edit → AHEAD, exit 1.
	if err := os.WriteFile(filepath.Join(e.dir, "results/meta.csv"), []byte("edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code = e.run("status", "--output=json")
	if code != 1 || !strings.Contains(stdout, "AHEAD") {
		t.Errorf("want AHEAD exit 1, got code=%d:\n%s", code, stdout)
	}
}

func TestOpen_DatasetRecordURL(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	mustPublish(t, e, "counts")

	stdout, stderr, code := e.run("open", "counts", "--output=json")
	if code != 0 {
		t.Fatalf("open: code=%d %s", code, stderr)
	}
	var res struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("open JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(res.URL, "/records/") || !strings.HasPrefix(res.URL, e.srv.URL()) {
		t.Errorf("open URL = %q", res.URL)
	}
}

// The pin is the source of truth for a pinned pull: when the record's
// listing contradicts the pinned MD5 (the live Dataverse rename bug put
// v1's file at v2's key), pull must fail loudly, not silently deliver
// wrong bytes and clobber the pin.
func TestDatasetPull_PinMismatchFailsLoudly(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	mustPublish(t, e, "counts")

	// Corrupt the pin: the manifest now claims different pinned bytes
	// than the record actually serves.
	p := filepath.Join(e.dir, ".datapin", "datapin.toml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`md5 = ['"][0-9a-f]{32}['"]`)
	corrupted := re.ReplaceAllString(string(data), `md5 = '00000000000000000000000000000000'`)
	if corrupted == string(data) {
		t.Fatal("no pinned md5 found to corrupt")
	}
	if err := os.WriteFile(p, []byte(corrupted), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.dir, "results/counts.csv")); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := e.run("pull", "counts")
	if code == 0 || !strings.Contains(stderr, "pin") {
		t.Fatalf("pull with contradicted pin: code=%d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "results/counts.csv")); !os.IsNotExist(err) {
		t.Fatal("pull wrote bytes despite the pin mismatch")
	}
}

// The no-manifest hint must offer the publish-first path (onboard), not
// only the OSF-shaped 'init <project-id>' — a researcher publishing to
// Zenodo has no OSF GUID to type (issue #40's docs/UX gate).
func TestNoManifest_HintOffersOnboard(t *testing.T) {
	e := newInvenioEnv(t)
	for _, cmd := range []string{"status", "sync"} {
		_, stderr, code := e.run(cmd)
		if code == 0 {
			t.Fatalf("%s without a manifest exited 0", cmd)
		}
		if !strings.Contains(stderr, "datapin onboard") {
			t.Errorf("%s hint does not mention 'datapin onboard':\n%s", cmd, stderr)
		}
	}
}
