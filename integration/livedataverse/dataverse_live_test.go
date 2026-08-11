//go:build live

// Package livedataverse exercises the publish pipeline against a real
// Dataverse instance (demo.dataverse.org by default). Compiled only under
// `-tags live`; the whole suite skips unless DATAVERSE_DEMO_TOKEN is set.
//
// ⚠ First-live-run caveat (docs/decisions.md D28): the dataverse driver
// was built against fakedataverse, which encodes DOCUMENTED behavior only.
// Treat failures here as findings about where real Dataverse diverges, and
// fix the fake and the driver together — the fakeosf/Zenodo lesson.
//
// The target collection rides on DATAVERSE_DEMO_URL
// (default https://demo.dataverse.org, i.e. the root collection). If the
// token's account cannot create datasets in root, point the URL at a
// collection it owns: https://demo.dataverse.org/dataverse/<alias>.
//
// Residue: published datasets on the demo instance cannot be deleted by a
// regular account. Titles carry a unique datapin-ci run id, and the demo
// instance is periodically wiped, so residue is identifiable and harmless.
// Nothing may assume datasets persist between runs.
//
// Run privately:
//
//	DATAVERSE_DEMO_TOKEN=… go test -tags live -count=1 -v ./integration/livedataverse/...
package livedataverse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var binaryPath string

func demoURL() string {
	if u := os.Getenv("DATAVERSE_DEMO_URL"); u != "" {
		return u
	}
	return "https://demo.dataverse.org"
}

func TestMain(m *testing.M) {
	if os.Getenv("DATAVERSE_DEMO_TOKEN") == "" {
		fmt.Fprintln(os.Stderr, "livedataverse: DATAVERSE_DEMO_TOKEN unset — skipping Dataverse live suite")
		os.Exit(0)
	}
	wd, _ := os.Getwd()
	repoRoot, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tmp, err := os.MkdirTemp("", "datapin-livedataverse-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin := filepath.Join(tmp, "datapin")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "livedataverse: build: %s\n", out)
		os.Exit(1)
	}
	binaryPath = bin
	os.Exit(m.Run())
}

type liveEnv struct {
	t   *testing.T
	dir string
}

func newLiveEnv(t *testing.T) *liveEnv {
	return &liveEnv{t: t, dir: t.TempDir()}
}

func (e *liveEnv) run(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(),
		"HOME="+e.dir,
		"XDG_CONFIG_HOME="+filepath.Join(e.dir, ".config"),
		// The per-remote token ladder picks this up for the "demo" remote.
		"DATAPIN_TOKEN_DEMO="+os.Getenv("DATAVERSE_DEMO_TOKEN"),
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	return outBuf.String(), errBuf.String(), code
}

// TestLiveDataverse_PublishLifecycle drives first-publish → idempotent
// republish → new-version → pull against a real Dataverse — the same
// lifecycle the fake-backed integration test pins, so any assertion that
// fails here and passes there is a documented-vs-real divergence (D28).
func TestLiveDataverse_PublishLifecycle(t *testing.T) {
	e := newLiveEnv(t)
	runID := fmt.Sprintf("datapin-ci-%d-%d", time.Now().UnixNano(), os.Getpid())

	if _, stderr, code := e.run("remote", "add", demoURL(),
		"--name", "demo", "--kind", "dataverse", "--no-keychain"); code != 0 {
		t.Fatalf("remote add: %s", stderr)
	}

	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(e.dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// A nested key exercises the directoryLabel mapping on a real instance.
	write("results/tables/data.csv", "x,y\n1,2\n"+runID+"\n")
	write(".datapin/datapin.toml", fmt.Sprintf(`
schema = 2

[project]
default_archive = "demo"

[[datasets]]
slug = "live"

  [datasets.metadata]
  title = "%s live-tier lifecycle test"
  description = "Created by datapin's live test suite; safe to delete."
  license = "CC0-1.0"
  keywords = ["datapin-ci"]
  [[datasets.metadata.creators]]
  name = "CI, Datapin"

  [[datasets.files]]
  local = "results/tables/data.csv"
  key   = "tables/data.csv"
`, runID))

	type pubJSON struct {
		State      string `json:"state"`
		Version    int    `json:"version"`
		DOI        string `json:"doi"`
		ConceptDOI string `json:"concept_doi"`
	}
	publish := func() pubJSON {
		t.Helper()
		stdout, stderr, code := e.run("publish", "live", "--yes", "--output=json")
		if code != 0 {
			t.Fatalf("publish (%d): %s\n%s", code, stdout, stderr)
		}
		var out []pubJSON
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("publish JSON: %v\n%s", err, stdout)
		}
		return out[0]
	}

	v1 := publish()
	if v1.State != "PUBLISHED" || v1.Version != 1 || v1.DOI == "" {
		t.Fatalf("v1 = %+v", v1)
	}
	// Dataverse mints one DOI for every version (D30).
	if v1.ConceptDOI != v1.DOI {
		t.Fatalf("concept DOI %q != version DOI %q", v1.ConceptDOI, v1.DOI)
	}

	// Idempotent republish must be a no-op against the real instance too.
	if again := publish(); again.State != "IN_SYNC" {
		t.Fatalf("republish = %+v, want IN_SYNC", again)
	}

	// Change → version 2 under the same DOI.
	write("results/tables/data.csv", "x,y\n1,3\n"+runID+"-v2\n")
	v2 := publish()
	if v2.State != "PUBLISHED" || v2.Version != 2 || v2.DOI != v1.DOI {
		t.Fatalf("v2 = %+v (v1 = %+v; want version 2 under the same DOI)", v2, v1)
	}

	// Pull round trip: blow the local file away and restore pinned bytes.
	if err := os.Remove(filepath.Join(e.dir, "results/tables/data.csv")); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, code := e.run("pull", "live", "--output=json"); code != 0 {
		t.Fatalf("pull (%d): %s\n%s", code, stdout, stderr)
	}
	got, err := os.ReadFile(filepath.Join(e.dir, "results/tables/data.csv"))
	if err != nil || !strings.Contains(string(got), runID+"-v2") {
		t.Fatalf("pulled bytes = %q, err=%v", got, err)
	}

	// The version chain is visible, every row under the one DOI.
	stdout, stderr, code := e.run("versions", "live", "--output=json")
	if code != 0 {
		t.Fatalf("versions (%d): %s", code, stderr)
	}
	if !strings.Contains(stdout, v1.DOI) {
		t.Errorf("versions output missing DOI %s: %s", v1.DOI, stdout)
	}
}
