//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/testutil/fakeinvenio"
)

// invenioEnv wraps a temp dir and a fakeinvenio server for archive tests.
type invenioEnv struct {
	t   *testing.T
	srv *fakeinvenio.Server
	dir string
}

const invenioTestToken = "integ-token"

func newInvenioEnv(t *testing.T) *invenioEnv {
	t.Helper()
	srv := fakeinvenio.New(invenioTestToken)
	t.Cleanup(srv.Close)
	return &invenioEnv{t: t, srv: srv, dir: t.TempDir()}
}

// run executes datapin isolated in the env's temp HOME/XDG.
func (e *invenioEnv) run(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.dir
	cmd.Env = append(hermeticEnv(),
		"HOME="+e.dir,
		"XDG_CONFIG_HOME="+filepath.Join(e.dir, ".config"),
		"OSF_TOKEN=", // archive tests never talk to OSF
	)
	cmd.Env = append(cmd.Env, coverEnv()...)
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

func TestRemote_AddListRemove(t *testing.T) {
	e := newInvenioEnv(t)

	stdout, stderr, code := e.run("remote", "add", e.srv.URL(),
		"--name", "sandbox", "--token-value", invenioTestToken, "--no-keychain")
	if code != 0 {
		t.Fatalf("remote add failed (%d): %s%s", code, stdout, stderr)
	}

	stdout, stderr, code = e.run("remote", "ls", "--output=json")
	if code != 0 {
		t.Fatalf("remote ls failed (%d): %s", code, stderr)
	}
	var rows []struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		URL      string `json:"url"`
		HasToken bool   `json:"has_token"`
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("remote ls JSON: %v\n%s", err, stdout)
	}
	if len(rows) != 1 || rows[0].Name != "sandbox" || rows[0].Kind != "invenio" || !rows[0].HasToken {
		t.Fatalf("remote ls = %+v", rows)
	}

	if _, stderr, code = e.run("remote", "rm", "sandbox"); code != 0 {
		t.Fatalf("remote rm failed (%d): %s", code, stderr)
	}
	stdout, _, _ = e.run("remote", "ls", "--output=json")
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) != 0 {
		t.Fatalf("after rm: %s", stdout)
	}
}

func TestRemote_AddProbesURL(t *testing.T) {
	e := newInvenioEnv(t)

	// A dead URL fails the probe...
	_, stderr, code := e.run("remote", "add", "http://127.0.0.1:1", "--name", "dead")
	if code == 0 {
		t.Fatal("remote add against a dead URL must fail")
	}
	if !strings.Contains(stderr, "no-verify") {
		t.Errorf("error should point at --no-verify, got: %s", stderr)
	}

	// ...unless --no-verify is given.
	if _, stderr, code := e.run("remote", "add", "http://127.0.0.1:1", "--name", "dead", "--no-verify"); code != 0 {
		t.Fatalf("remote add --no-verify failed (%d): %s", code, stderr)
	}
}

func TestRemote_AddDuplicateFails(t *testing.T) {
	e := newInvenioEnv(t)
	if _, stderr, code := e.run("remote", "add", e.srv.URL(), "--name", "z"); code != 0 {
		t.Fatalf("first add failed: %s", stderr)
	}
	if _, _, code := e.run("remote", "add", e.srv.URL(), "--name", "z"); code == 0 {
		t.Fatal("duplicate remote add must fail")
	}
}
