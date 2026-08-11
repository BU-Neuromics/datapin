//go:build live

// Package livezenodo exercises the publish pipeline against the real
// sandbox.zenodo.org. Compiled only under `-tags live`; the whole suite
// skips unless ZENODO_SANDBOX_TOKEN is set. This is the tier that catches
// where real Zenodo diverges from fakeinvenio's assumptions (the fakeosf
// lesson, plan §6).
//
// Sandbox records created here cannot be deleted via the API; the sandbox
// is wipeable and titles carry a unique datapin-ci prefix, so residue is
// identifiable and harmless. Nothing may assume records persist between
// runs.
//
// Run privately:
//
//	ZENODO_SANDBOX_TOKEN=… go test -tags live -count=1 -v ./integration/livezenodo/...
package livezenodo

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

func TestMain(m *testing.M) {
	if os.Getenv("ZENODO_SANDBOX_TOKEN") == "" {
		fmt.Fprintln(os.Stderr, "livezenodo: ZENODO_SANDBOX_TOKEN unset — skipping Zenodo sandbox suite")
		os.Exit(0)
	}
	wd, _ := os.Getwd()
	repoRoot, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tmp, err := os.MkdirTemp("", "datapin-livezenodo-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin := filepath.Join(tmp, "datapin")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "livezenodo: build: %s\n", out)
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
		// The per-remote token ladder picks this up for the "sandbox" remote.
		"DATAPIN_TOKEN_SANDBOX="+os.Getenv("ZENODO_SANDBOX_TOKEN"),
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

// TestLiveZenodo_PublishLifecycle drives the full first-publish → change →
// new-version → pull round trip against the real sandbox — the Phase 1
// exit criterion (plan §8).
func TestLiveZenodo_PublishLifecycle(t *testing.T) {
	e := newLiveEnv(t)
	runID := fmt.Sprintf("datapin-ci-%d-%d", time.Now().UnixNano(), os.Getpid())

	if _, stderr, code := e.run("remote", "add", "https://sandbox.zenodo.org", "--name", "sandbox", "--no-keychain"); code != 0 {
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
	write("results/data.csv", "x,y\n1,2\n"+runID+"\n")
	write(".datapin/datapin.toml", fmt.Sprintf(`
schema = 2

[project]
default_archive = "sandbox"

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
  local = "results/data.csv"
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
	if v1.State != "PUBLISHED" || v1.Version != 1 || !strings.HasPrefix(v1.DOI, "10.5072/") {
		t.Fatalf("v1 = %+v", v1)
	}

	// Idempotent republish must be a no-op against the real sandbox too.
	if again := publish(); again.State != "IN_SYNC" {
		t.Fatalf("republish = %+v, want IN_SYNC", again)
	}

	// Change → v2 with a fresh version DOI, stable concept DOI.
	write("results/data.csv", "x,y\n1,3\n"+runID+"-v2\n")
	v2 := publish()
	if v2.State != "PUBLISHED" || v2.Version != 2 || v2.DOI == v1.DOI || v2.ConceptDOI != v1.ConceptDOI {
		t.Fatalf("v2 = %+v (v1 = %+v)", v2, v1)
	}

	// Pull round trip: blow the local file away and restore pinned bytes.
	if err := os.Remove(filepath.Join(e.dir, "results/data.csv")); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, code := e.run("pull", "live", "--output=json"); code != 0 {
		t.Fatalf("pull (%d): %s\n%s", code, stdout, stderr)
	}
	got, err := os.ReadFile(filepath.Join(e.dir, "results/data.csv"))
	if err != nil || !strings.Contains(string(got), runID+"-v2") {
		t.Fatalf("pulled bytes = %q, err=%v", got, err)
	}

	// The version chain is visible with the pin on the latest.
	stdout, stderr, code := e.run("versions", "live", "--output=json")
	if code != 0 {
		t.Fatalf("versions (%d): %s", code, stderr)
	}
	if !strings.Contains(stdout, v2.DOI) || !strings.Contains(stdout, v1.DOI) {
		t.Errorf("versions output missing DOIs: %s", stdout)
	}
}
