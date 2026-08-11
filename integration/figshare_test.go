//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/testutil/fakefigshare"
)

const figshareTestToken = "fig-integ-token"

// figshareEnv wraps a temp dir and a fakefigshare server.
type figshareEnv struct {
	t   *testing.T
	srv *fakefigshare.Server
	dir string
}

func newFigshareEnv(t *testing.T) *figshareEnv {
	t.Helper()
	srv := fakefigshare.New(figshareTestToken)
	t.Cleanup(srv.Close)
	return &figshareEnv{t: t, srv: srv, dir: t.TempDir()}
}

func (e *figshareEnv) run(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.dir
	cmd.Env = append(hermeticEnv(),
		"HOME="+e.dir,
		"XDG_CONFIG_HOME="+filepath.Join(e.dir, ".config"),
		"OSF_TOKEN=",
	)
	cmd.Env = append(cmd.Env, coverEnv()...)
	var outBuf, errBuf strings.Builder
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

func TestFigshare_PublishLifecycle(t *testing.T) {
	e := newFigshareEnv(t)

	if _, stderr, code := e.run("remote", "add", e.srv.URL(),
		"--name", "figs", "--kind", "figshare",
		"--token-value", figshareTestToken, "--no-keychain"); code != 0 {
		t.Fatalf("remote add --kind figshare: %s", stderr)
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
	write("results/data.csv", "x,y\n1,2\n")
	write(".datapin/datapin.toml", `
schema = 2

[project]
default_archive = "figs"

[[datasets]]
slug = "figset"

  [datasets.metadata]
  title = "figshare integration dataset"
  license = "CC0-1.0"
  [[datasets.metadata.creators]]
  name = "Tester, Trusty"

  [[datasets.files]]
  local = "results/data.csv"
`)

	publish := func() publishJSON {
		t.Helper()
		stdout, stderr, code := e.run("publish", "figset", "--yes", "--output=json")
		if code != 0 {
			t.Fatalf("publish (%d): %s\n%s", code, stdout, stderr)
		}
		var out []publishJSON
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("publish JSON: %v\n%s", err, stdout)
		}
		return out[0]
	}

	v1 := publish()
	if v1.State != "PUBLISHED" || v1.Version != 1 {
		t.Fatalf("v1 = %+v", v1)
	}
	if !strings.HasSuffix(v1.DOI, ".v1") {
		t.Errorf("figshare version DOI = %q, want .v1 suffix", v1.DOI)
	}
	if v1.ConceptDOI != strings.TrimSuffix(v1.DOI, ".v1") {
		t.Errorf("concept DOI = %q for version DOI %q", v1.ConceptDOI, v1.DOI)
	}

	// Idempotent republish.
	if again := publish(); again.State != "IN_SYNC" {
		t.Fatalf("republish = %+v", again)
	}

	// Change → v2 (no files-import on figshare; the draft carries files).
	write("results/data.csv", "x,y\n1,3\n")
	v2 := publish()
	if v2.State != "PUBLISHED" || v2.Version != 2 || !strings.HasSuffix(v2.DOI, ".v2") {
		t.Fatalf("v2 = %+v", v2)
	}
	if v2.ConceptDOI != v1.ConceptDOI {
		t.Error("concept DOI must be stable")
	}

	// Pull the pinned v2 bytes back after deleting them locally.
	if err := os.Remove(filepath.Join(e.dir, "results/data.csv")); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, code := e.run("pull", "figset", "--output=json"); code != 0 {
		t.Fatalf("pull: %s\n%s", stdout, stderr)
	}
	got, _ := os.ReadFile(filepath.Join(e.dir, "results/data.csv"))
	if string(got) != "x,y\n1,3\n" {
		t.Errorf("pulled bytes = %q", got)
	}

	// versions shows the chain with the pin on v2.
	stdout, stderr, code := e.run("versions", "figset", "--output=json")
	if code != 0 {
		t.Fatalf("versions: %s", stderr)
	}
	if !strings.Contains(stdout, `.v1"`) || !strings.Contains(stdout, `.v2"`) {
		t.Errorf("versions output: %s", stdout)
	}
}
