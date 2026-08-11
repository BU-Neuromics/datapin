//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/testutil/fakedataverse"
)

const dataverseTestToken = "dv-integ-token"

type dataverseEnv struct {
	t   *testing.T
	srv *fakedataverse.Server
	dir string
}

func newDataverseEnv(t *testing.T) *dataverseEnv {
	t.Helper()
	srv := fakedataverse.New(dataverseTestToken)
	t.Cleanup(srv.Close)
	return &dataverseEnv{t: t, srv: srv, dir: t.TempDir()}
}

func (e *dataverseEnv) run(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(),
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

func TestDataverse_PublishLifecycle(t *testing.T) {
	e := newDataverseEnv(t)

	if _, stderr, code := e.run("remote", "add", e.srv.URL()+"/dataverse/mylab",
		"--name", "dv", "--kind", "dataverse",
		"--token-value", dataverseTestToken, "--no-keychain"); code != 0 {
		t.Fatalf("remote add --kind dataverse: %s", stderr)
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
	// A nested local path: the key carries a "/" and lands as
	// directoryLabel on Dataverse.
	write("results/tables/data.csv", "x,y\n1,2\n")
	write(".datapin/datapin.toml", `
schema = 2

[project]
default_archive = "dv"

[[datasets]]
slug = "dvset"

  [datasets.metadata]
  title = "dataverse integration dataset"
  license = "CC0-1.0"
  [[datasets.metadata.creators]]
  name = "Tester, Trusty"

  [[datasets.files]]
  local = "results/tables/data.csv"
  key   = "tables/data.csv"
`)

	publish := func() publishJSON {
		t.Helper()
		stdout, stderr, code := e.run("publish", "dvset", "--yes", "--output=json")
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
	if !strings.HasPrefix(v1.DOI, "10.5072/FK2/") {
		t.Errorf("DOI = %q", v1.DOI)
	}
	// Dataverse: one DOI for every version.
	if v1.ConceptDOI != v1.DOI {
		t.Errorf("dataverse concept DOI %q != version DOI %q", v1.ConceptDOI, v1.DOI)
	}

	if again := publish(); again.State != "IN_SYNC" {
		t.Fatalf("republish = %+v", again)
	}

	write("results/tables/data.csv", "x,y\n1,3\n")
	v2 := publish()
	if v2.State != "PUBLISHED" || v2.Version != 2 || v2.DOI != v1.DOI {
		t.Fatalf("v2 = %+v (want same DOI, version 2)", v2)
	}

	// Round trip the nested key.
	if err := os.Remove(filepath.Join(e.dir, "results/tables/data.csv")); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, code := e.run("pull", "dvset", "--output=json"); code != 0 {
		t.Fatalf("pull: %s\n%s", stdout, stderr)
	}
	got, _ := os.ReadFile(filepath.Join(e.dir, "results/tables/data.csv"))
	if string(got) != "x,y\n1,3\n" {
		t.Errorf("pulled bytes = %q", got)
	}
}
