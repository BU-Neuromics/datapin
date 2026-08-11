//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wsEnv is a temp working dir plus a dir-kind workspace remote.
type wsEnv struct {
	t      *testing.T
	dir    string
	remote string
}

func newWSEnv(t *testing.T) *wsEnv {
	t.Helper()
	return &wsEnv{t: t, dir: t.TempDir(), remote: t.TempDir()}
}

func (e *wsEnv) run(args ...string) (stdout, stderr string, code int) {
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

func (e *wsEnv) setup(t *testing.T) {
	t.Helper()
	if _, stderr, code := e.run("remote", "add", e.remote, "--name", "nas", "--kind", "dir"); code != 0 {
		t.Fatalf("remote add --kind dir: %s", stderr)
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
	write("results/counts.csv", "gene,count\nA,1\n")
	write(".datapin/datapin.toml", `
schema = 2

[project]
default_workspace = "nas"

[[datasets]]
slug = "counts"
  [[datasets.files]]
  local = "results/counts.csv"
`)
}

func TestWorkspace_PushPullRoundTrip(t *testing.T) {
	e := newWSEnv(t)
	e.setup(t)

	// Push v1 — note: no metadata needed for workspace sync (plan §4.7).
	if stdout, stderr, code := e.run("push", "counts", "--output=json"); code != 0 {
		t.Fatalf("push (%d): %s\n%s", code, stdout, stderr)
	}
	// Idempotent re-push.
	stdout, _, code := e.run("push", "counts", "--output=json")
	if code != 0 || !strings.Contains(stdout, `"unchanged"`) {
		t.Fatalf("re-push not idempotent: %s", stdout)
	}

	// Change and push v2.
	if err := os.WriteFile(filepath.Join(e.dir, "results/counts.csv"), []byte("gene,count\nA,2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := e.run("push", "counts"); code != 0 {
		t.Fatalf("push v2: %s", stderr)
	}

	// The laptop side: wipe local bytes, pull workspace state.
	if err := os.Remove(filepath.Join(e.dir, "results/counts.csv")); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, code := e.run("pull", "counts", "--workspace", "--output=json"); code != 0 {
		t.Fatalf("pull --workspace (%d): %s\n%s", code, stdout, stderr)
	}
	got, _ := os.ReadFile(filepath.Join(e.dir, "results/counts.csv"))
	if string(got) != "gene,count\nA,2\n" {
		t.Errorf("pulled = %q", got)
	}

	// Unpublished dataset with a workspace: bare pull uses the workspace.
	if err := os.Remove(filepath.Join(e.dir, "results/counts.csv")); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := e.run("pull", "counts"); code != 0 {
		t.Fatalf("bare pull fallback: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "results/counts.csv")); err != nil {
		t.Error("bare pull did not restore the file from the workspace")
	}
}

func TestWorkspace_VersionsRevertGC(t *testing.T) {
	e := newWSEnv(t)
	e.setup(t)

	push := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(e.dir, "results/counts.csv"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if _, stderr, code := e.run("push", "counts"); code != 0 {
			t.Fatalf("push: %s", stderr)
		}
	}
	push("v1\n")
	push("v2\n")
	push("v3\n")

	// versions <slug>/<key> lists the journal.
	stdout, stderr, code := e.run("versions", "counts/counts.csv", "--output=json")
	if code != 0 {
		t.Fatalf("versions (%d): %s", code, stderr)
	}
	var vres struct {
		Events []struct {
			Seq         int    `json:"seq"`
			Action      string `json:"action"`
			Recoverable bool   `json:"recoverable"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(stdout), &vres); err != nil {
		t.Fatalf("versions JSON: %v\n%s", err, stdout)
	}
	if len(vres.Events) != 3 || !vres.Events[0].Recoverable {
		t.Fatalf("events = %+v", vres.Events)
	}

	// Revert to v1 (a journaled event), pull, and check the bytes.
	if _, stderr, code := e.run("revert", "counts/counts.csv", "--to", "1", "--reason", "bad batch"); code != 0 {
		t.Fatalf("revert (%d): %s", code, stderr)
	}
	if _, stderr, code := e.run("pull", "counts", "--workspace"); code != 0 {
		t.Fatalf("pull after revert: %s", stderr)
	}
	got, _ := os.ReadFile(filepath.Join(e.dir, "results/counts.csv"))
	if string(got) != "v1\n" {
		t.Errorf("after revert+pull = %q", got)
	}

	// GC with keep=1 reclaims old archives but never rewrites the journal.
	if stdout, stderr, code := e.run("gc", "--keep", "1", "--output=json"); code != 0 {
		t.Fatalf("gc (%d): %s\n%s", code, stdout, stderr)
	}
	stdout, _, _ = e.run("versions", "counts/counts.csv", "--output=json")
	if err := json.Unmarshal([]byte(stdout), &vres); err != nil {
		t.Fatal(err)
	}
	if len(vres.Events) != 4 {
		t.Errorf("journal rewritten by gc: %+v", vres.Events)
	}
}
