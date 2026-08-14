//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `datapin status` reports the workspace track alongside the archive one
// (#57). The wsEnv harness (workspace_test.go) gives us a dir-kind
// workspace remote and a one-file dataset.

// wsStatusItem is the dataset row of `datapin status --output=json`,
// including the additive workspace fields.
type wsStatusItem struct {
	Path            string `json:"path"`
	Kind            string `json:"kind"`
	State           string `json:"state"`
	DeclaredVersion int    `json:"declared_version"`
	WorkspaceRemote string `json:"workspace_remote"`
	WorkspaceState  string `json:"workspace_state"`
	WorkspaceFiles  []struct {
		Local   string `json:"local"`
		Key     string `json:"key"`
		State   string `json:"state"`
		Version int    `json:"version"`
	} `json:"workspace_files"`
}

func (e *wsEnv) statusJSON(t *testing.T, args ...string) []wsStatusItem {
	t.Helper()
	stdout, stderr, _ := e.run(append([]string{"status", "--output=json"}, args...)...)
	var items []wsStatusItem
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("status JSON: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	return items
}

func TestStatus_WorkspaceStates(t *testing.T) {
	e := newWSEnv(t)
	e.setup(t)
	counts := filepath.Join(e.dir, "results/counts.csv")

	// Never pushed: the workspace has nothing. That is the absence of an
	// optional copy, not drift.
	items := e.statusJSON(t)
	if len(items) != 1 || items[0].Kind != "dataset" {
		t.Fatalf("status items = %+v", items)
	}
	if items[0].WorkspaceRemote != "nas" || items[0].WorkspaceState != "NOT_PUSHED" {
		t.Errorf("before any push: remote=%q state=%q", items[0].WorkspaceRemote, items[0].WorkspaceState)
	}
	// The original contract fields are untouched.
	if items[0].State != "NOT_PUBLISHED" || items[0].Path != "counts" {
		t.Errorf("archive fields changed: %+v", items[0])
	}

	// Pushed: in sync with the workspace.
	if _, stderr, code := e.run("push", "counts"); code != 0 {
		t.Fatalf("push: %s", stderr)
	}
	items = e.statusJSON(t)
	if items[0].WorkspaceState != "IN_SYNC" {
		t.Errorf("after push: workspace_state = %q, want IN_SYNC", items[0].WorkspaceState)
	}
	if len(items[0].WorkspaceFiles) != 1 || items[0].WorkspaceFiles[0].Key != "counts.csv" ||
		items[0].WorkspaceFiles[0].State != "IN_SYNC" {
		t.Errorf("per-key breakdown = %+v", items[0].WorkspaceFiles)
	}

	// A local edit the workspace has never seen → AHEAD, and the run exits
	// non-zero because that is real drift.
	if err := os.WriteFile(counts, []byte("gene,count\nA,99\n"), 0644); err != nil {
		t.Fatal(err)
	}
	items = e.statusJSON(t)
	if items[0].WorkspaceState != "AHEAD" {
		t.Errorf("after local edit: workspace_state = %q, want AHEAD", items[0].WorkspaceState)
	}
	if _, _, code := e.run("status"); code == 0 {
		t.Error("workspace drift must make status exit non-zero")
	}

	// Someone pushes newer bytes while our local copy sits at an older
	// journaled version: BEHIND, provable from the journal.
	if _, stderr, code := e.run("push", "counts"); code != 0 { // our edit becomes v2
		t.Fatalf("push v2: %s", stderr)
	}
	if err := os.WriteFile(counts, []byte("gene,count\nA,1\n"), 0644); err != nil { // back to v1's bytes
		t.Fatal(err)
	}
	items = e.statusJSON(t)
	if items[0].WorkspaceState != "BEHIND" {
		t.Errorf("local at an older journaled version: workspace_state = %q, want BEHIND", items[0].WorkspaceState)
	}
	if items[0].WorkspaceFiles[0].Version != 2 {
		t.Errorf("workspace head version = %d, want 2", items[0].WorkspaceFiles[0].Version)
	}

	// Local file gone entirely: MISSING on the workspace track.
	if err := os.Remove(counts); err != nil {
		t.Fatal(err)
	}
	items = e.statusJSON(t)
	if items[0].WorkspaceState != "MISSING" {
		t.Errorf("local file removed: workspace_state = %q, want MISSING", items[0].WorkspaceState)
	}

	// --no-check-remote does no remote I/O at all, so it reports nothing
	// about the workspace rather than guessing.
	items = e.statusJSON(t, "--no-check-remote")
	if items[0].WorkspaceState != "" || items[0].WorkspaceRemote != "" || items[0].WorkspaceFiles != nil {
		t.Errorf("--no-check-remote leaked workspace state: %+v", items[0])
	}
}

// The human table gains a WORKSPACE column only when a dataset can fill
// it, and the workspace remedy command appears in the row's detail.
func TestStatus_WorkspaceColumn(t *testing.T) {
	e := newWSEnv(t)
	e.setup(t)

	stdout, _, _ := e.run("status")
	if !strings.Contains(stdout, "WORKSPACE") {
		t.Errorf("expected a WORKSPACE column:\n%s", stdout)
	}
	if !strings.Contains(stdout, `workspace "nas"`) || !strings.Contains(stdout, "datapin push counts") {
		t.Errorf("expected the workspace clause and its remedy:\n%s", stdout)
	}

	// --no-check-remote keeps the original four-column table.
	stdout, _, _ = e.run("status", "--no-check-remote")
	if strings.Contains(stdout, "WORKSPACE") {
		t.Errorf("--no-check-remote should not add the column:\n%s", stdout)
	}
}

// A dataset with no workspace remote must render cleanly, attempt no I/O,
// and keep the pre-#57 table — the common case today.
func TestStatus_NoWorkspaceConfigured(t *testing.T) {
	e := newWSEnv(t)
	if err := os.MkdirAll(filepath.Join(e.dir, ".datapin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "results.csv"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	toml := "schema = 2\n\n[[datasets]]\nslug = \"counts\"\n  [[datasets.files]]\n  local = \"results.csv\"\n"
	if err := os.WriteFile(filepath.Join(e.dir, ".datapin/datapin.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, _ := e.run("status")
	if strings.Contains(stdout, "WORKSPACE") || strings.Contains(stdout, `workspace "`) {
		t.Errorf("no workspace configured, but status mentions one:\n%s", stdout)
	}
	if strings.Contains(stderr, "workspace") {
		t.Errorf("no workspace configured, but status warned about one:\n%s", stderr)
	}

	items := e.statusJSON(t)
	if len(items) != 1 || items[0].WorkspaceState != "" || items[0].WorkspaceRemote != "" {
		t.Errorf("workspace fields should be absent: %+v", items)
	}
}

// A workspace remote named in the manifest but missing from config.toml is
// a warning, not a failed run: status still reports the archive answer.
func TestStatus_UnreachableWorkspaceIsAWarning(t *testing.T) {
	e := newWSEnv(t)
	e.setup(t)
	// Drop the remote from the config, leaving default_workspace = "nas".
	if _, stderr, code := e.run("remote", "rm", "nas"); code != 0 {
		t.Fatalf("remote rm: %s", stderr)
	}

	stdout, stderr, code := e.run("status")
	if code == 0 {
		t.Error("an unverifiable workspace must not exit 0")
	}
	if !strings.Contains(stderr, "workspace") {
		t.Errorf("expected a warning on stderr:\n%s", stderr)
	}
	if !strings.Contains(stdout, "dataset") {
		t.Errorf("the archive row must still be reported:\n%s", stdout)
	}

	items := e.statusJSON(t)
	if items[0].WorkspaceState != "UNKNOWN" {
		t.Errorf("workspace_state = %q, want UNKNOWN", items[0].WorkspaceState)
	}
}
