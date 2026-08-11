//go:build live

// Package live holds integration tests that exercise a real OSF project over the
// network. They are compiled only under `-tags live` and skip unless the OSF test
// credentials are present in the environment:
//
//	OSF_TEST_TOKEN     personal access token with write access to the test project
//	OSF_TEST_PROJECT   GUID of a private test project
//	OSF_TEST_COMPONENT GUID of a child component (optional; cross-project tests)
//
// Run privately:
//
//	OSF_TEST_TOKEN=… OSF_TEST_PROJECT=… OSF_TEST_COMPONENT=… \
//	  go test -tags live -count=1 -v ./integration/live/...
//
// Every test writes under a unique remote folder and deletes it on cleanup, so
// runs are repeatable, parallel-safe, and leave no residue in the test project.
package live

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// binaryPath is the datapin binary built once in TestMain and shared across tests.
var binaryPath string

func TestMain(m *testing.M) {
	// Skip the (slow) build entirely when no credentials are configured — the
	// whole suite would skip anyway.
	if os.Getenv("OSF_TEST_TOKEN") == "" || os.Getenv("OSF_TEST_PROJECT") == "" {
		fmt.Fprintln(os.Stderr, "live: OSF_TEST_TOKEN/OSF_TEST_PROJECT unset — skipping live suite")
		os.Exit(0)
	}
	bin, err := buildBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "live: build binary: %v\n", err)
		os.Exit(1)
	}
	binaryPath = bin
	os.Exit(m.Run())
}

func buildBinary() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	repoRoot, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "datapin-live-*")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(tmp, "datapin")
	buildArgs := []string{"build", "-o", bin, "."}
	if os.Getenv("DATAPIN_COVERDIR") != "" {
		buildArgs = []string{"build", "-cover", "-o", bin, "."}
	}
	cmd := exec.Command("go", buildArgs...)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build failed: %s", out)
	}
	return bin, nil
}

// liveEnv runs the real datapin binary against real OSF using the test PAT, in an
// isolated HOME so it never reads or writes the developer's config/keychain.
type liveEnv struct {
	t         *testing.T
	dir       string
	token     string
	project   string
	component string
}

// requireLive skips the test unless live credentials are configured.
func requireLive(t *testing.T) *liveEnv {
	t.Helper()
	token := os.Getenv("OSF_TEST_TOKEN")
	project := os.Getenv("OSF_TEST_PROJECT")
	if token == "" || project == "" {
		t.Skip("live OSF tests require OSF_TEST_TOKEN and OSF_TEST_PROJECT")
	}
	return &liveEnv{
		t:         t,
		dir:       t.TempDir(),
		token:     token,
		project:   project,
		component: os.Getenv("OSF_TEST_COMPONENT"),
	}
}

// requireComponent skips unless a component GUID is configured.
func (e *liveEnv) requireComponent() string {
	e.t.Helper()
	if e.component == "" {
		e.t.Skip("this test requires OSF_TEST_COMPONENT (a child component GUID)")
	}
	return e.component
}

// run executes datapin with args in the test working dir against live OSF.
func (e *liveEnv) run(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(),
		"OSF_TOKEN="+e.token,
		// Isolate from the developer's real keychain/config.
		"HOME="+e.dir,
		"XDG_CONFIG_HOME="+filepath.Join(e.dir, ".config"),
		// Guard against a leaked fake-server override: empty → live api.osf.io.
		"DATAPIN_API_BASE=",
		"DATAPIN_FILES_BASE=",
	)
	if dir := os.Getenv("DATAPIN_COVERDIR"); dir != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	return outBuf.String(), errBuf.String(), code
}

// mkTempRemoteDir creates a unique root-level folder in the given node and
// registers cleanup that deletes it (best effort) after the test.
func (e *liveEnv) mkTempRemoteDir(node string) string {
	e.t.Helper()
	dir := fmt.Sprintf("/datapin-ci-%d-%d", time.Now().UnixNano(), os.Getpid())
	if _, stderr, code := e.runEventually("mkdir", node+":"+dir, "--quiet"); code != 0 {
		e.t.Fatalf("mkdir %s:%s failed: %s", node, dir, stderr)
	}
	e.t.Cleanup(func() {
		e.runEventually("rm", node+":"+dir, "--yes", "--quiet")
	})
	return dir
}

// runEventually retries a datapin command until it exits 0 (or a cap is hit),
// tolerating OSF's eventual consistency: reads through the metadata API
// (api.osf.io) can transiently 404 for a short window after a Waterbutler write
// (files.osf.io), and the inconsistency is non-monotonic (a read may succeed,
// then the next fails). A command that fails on a transient 404 did no work, so
// retrying it is safe. Each attempt's network round-trip provides backoff.
func (e *liveEnv) runEventually(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	for i := 0; i < 40; i++ {
		stdout, stderr, code = e.run(args...)
		if code == 0 {
			return
		}
	}
	return
}

// waitGone polls until target no longer resolves (a delete has propagated to the
// metadata API). Deletes lag just like writes under OSF's eventual consistency.
func (e *liveEnv) waitGone(target string) {
	e.t.Helper()
	for i := 0; i < 40; i++ {
		if _, _, code := e.run("ls", target, "--output=json"); code != 0 {
			return
		}
	}
	e.t.Fatalf("timed out waiting for %s to be gone (metadata lag)", target)
}

// writeFile creates a file (and parent dirs) under the test working dir.
func (e *liveEnv) writeFile(name, content string) {
	e.t.Helper()
	p := filepath.Join(e.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatalf("write %s: %v", name, err)
	}
}

// readFile reads a file from the test working dir.
func (e *liveEnv) readFile(name string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(name)))
	if err != nil {
		e.t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}
