package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/workspace"
	"github.com/BU-Neuromics/datapin/internal/workspace/localdir"
)

// --- aggregation ------------------------------------------------------

// A dataset is one row, so per-key workspace states aggregate by a
// documented precedence. Keys that exist on neither side carry no
// workspace work and are excluded.
func TestAggregateWorkspaceState(t *testing.T) {
	k := func(state workspace.KeyState, localMD5 string) workspaceKeyStatus {
		return workspaceKeyStatus{State: state, LocalMD5: localMD5}
	}
	tests := []struct {
		name string
		keys []workspaceKeyStatus
		want string
	}{
		{"no files at all", nil, wsStateNotPushed},
		{"all in sync", []workspaceKeyStatus{
			k(workspace.KeyInSync, "a"), k(workspace.KeyInSync, "b"),
		}, wsStateInSync},
		{"one never pushed", []workspaceKeyStatus{
			k(workspace.KeyInSync, "a"), k(workspace.KeyNotPushed, "b"),
		}, wsStateNotPushed},
		{"absent on both sides carries no work", []workspaceKeyStatus{
			k(workspace.KeyInSync, "a"), k(workspace.KeyNotPushed, ""),
		}, wsStateInSync},
		{"every key absent on both sides", []workspaceKeyStatus{
			k(workspace.KeyNotPushed, ""),
		}, wsStateNotPushed},
		{"a collaborator pushed", []workspaceKeyStatus{
			k(workspace.KeyInSync, "a"), k(workspace.KeyBehind, "b"),
		}, wsStateBehind},
		{"local file gone", []workspaceKeyStatus{
			k(workspace.KeyInSync, "a"), k(workspace.KeyMissing, ""),
		}, wsStateMissing},
		{"local work to share", []workspaceKeyStatus{
			k(workspace.KeyInSync, "a"), k(workspace.KeyAhead, "b"),
		}, wsStateAhead},
		{"ahead outranks behind: both directions is divergence", []workspaceKeyStatus{
			k(workspace.KeyAhead, "a"), k(workspace.KeyBehind, "b"),
		}, wsStateDiverged},
		{"unpushed plus pullable is also divergence", []workspaceKeyStatus{
			k(workspace.KeyNotPushed, "a"), k(workspace.KeyMissing, ""),
		}, wsStateDiverged},
		{"ahead outranks a merely-unpushed key", []workspaceKeyStatus{
			k(workspace.KeyAhead, "a"), k(workspace.KeyNotPushed, "b"),
		}, wsStateAhead},
		{"behind outranks missing", []workspaceKeyStatus{
			k(workspace.KeyBehind, "a"), k(workspace.KeyMissing, ""),
		}, wsStateBehind},
	}
	for _, tt := range tests {
		if got := aggregateWorkspaceState(tt.keys); got != tt.want {
			t.Errorf("%s: aggregateWorkspaceState = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// The exit code means "is there work to do". Real drift counts; the mere
// absence of an optional workspace copy does not, so a published dataset
// that never used its workspace must not start failing CI.
func TestWorkspaceStateIsInSync(t *testing.T) {
	tests := map[string]bool{
		wsStateInSync:    true,
		wsStateNotPushed: true,
		wsStateNone:      true,
		wsStateBehind:    false,
		wsStateMissing:   false,
		wsStateAhead:     false,
		wsStateDiverged:  false,
		wsStateUnknown:   false,
	}
	for state, want := range tests {
		if got := workspaceStateIsInSync(state); got != want {
			t.Errorf("workspaceStateIsInSync(%q) = %v, want %v", state, got, want)
		}
	}
}

// --- detail text ------------------------------------------------------

func TestWorkspaceDetail(t *testing.T) {
	keys := []workspaceKeyStatus{
		{Key: "a.csv", State: workspace.KeyBehind, LocalMD5: "a", HeadSeq: 3},
		{Key: "b.csv", State: workspace.KeyInSync, LocalMD5: "b", HeadSeq: 1},
	}
	got := workspaceDetail(wsStateBehind, keys, "nas", "counts", nil)
	for _, want := range []string{`workspace "nas"`, "1 file", "datapin pull counts --workspace"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail %q does not mention %q", got, want)
		}
	}

	// AHEAD cannot distinguish local work from a three-way divergence
	// (there is no workspace baseline), so it must name both remedies.
	ahead := []workspaceKeyStatus{{Key: "a.csv", State: workspace.KeyAhead, LocalMD5: "z"}}
	got = workspaceDetail(wsStateAhead, ahead, "nas", "counts", nil)
	if !strings.Contains(got, "datapin push counts") || !strings.Contains(got, "--workspace") {
		t.Errorf("AHEAD detail must name both remedies, got %q", got)
	}

	// An unreachable workspace reports why, and never claims sync.
	got = workspaceDetail(wsStateUnknown, nil, "nas", "counts", context.DeadlineExceeded)
	if !strings.Contains(got, "context deadline exceeded") {
		t.Errorf("UNKNOWN detail should carry the error, got %q", got)
	}

	// No workspace configured: nothing to say, and no I/O was attempted.
	if got := workspaceDetail(wsStateNone, nil, "", "counts", nil); got != "" {
		t.Errorf("detail for an unconfigured workspace = %q, want empty", got)
	}
}

// The WORKSPACE column only appears when it can carry information: a
// legacy OSF-only manifest, or --no-check-remote, keeps today's table.
func TestStatusShowsWorkspaceColumn(t *testing.T) {
	withWS := &manifest.Manifest{Datasets: []manifest.Dataset{{Slug: "a", Workspace: "nas"}}}
	viaDefault := &manifest.Manifest{
		Project:  manifest.ProjectConfig{DefaultWorkspace: "nas"},
		Datasets: []manifest.Dataset{{Slug: "a"}},
	}
	noWS := &manifest.Manifest{Datasets: []manifest.Dataset{{Slug: "a"}}}
	osfOnly := &manifest.Manifest{Files: []manifest.Entry{{Local: "x"}}}

	tests := []struct {
		name          string
		m             *manifest.Manifest
		noCheckRemote bool
		wantHasColumn bool
	}{
		{"dataset names a workspace", withWS, false, true},
		{"project default_workspace", viaDefault, false, true},
		{"dataset without a workspace", noWS, false, false},
		{"legacy OSF manifest", osfOnly, false, false},
		{"--no-check-remote suppresses it", withWS, true, false},
	}
	for _, tt := range tests {
		if got := statusShowsWorkspaceColumn(tt.m, tt.noCheckRemote); got != tt.wantHasColumn {
			t.Errorf("%s: statusShowsWorkspaceColumn = %v, want %v", tt.name, got, tt.wantHasColumn)
		}
	}
}

// --- probing against a real (hermetic) workspace ----------------------

func newTestWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	store, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return workspace.New(store)
}

func wsPush(t *testing.T, ws *workspace.Workspace, key string, content []byte) {
	t.Helper()
	if _, err := ws.Push(context.Background(), key, bytes.NewReader(content), int64(len(content)), md5hex(content), "tester"); err != nil {
		t.Fatalf("Push(%s): %v", key, err)
	}
}

func TestDatasetWorkspaceStatus(t *testing.T) {
	ws := newTestWorkspace(t)
	wsPush(t, ws, "insync.csv", []byte("same"))
	wsPush(t, ws, "behind.csv", []byte("old"))
	wsPush(t, ws, "behind.csv", []byte("new")) // collaborator pushed v2
	wsPush(t, ws, "gone.csv", []byte("only remote"))

	ds := &manifest.Dataset{Slug: "counts", Files: []manifest.DatasetFile{
		{Local: "r/insync.csv", Key: "insync.csv"},
		{Local: "r/behind.csv", Key: "behind.csv"},
		{Local: "r/ahead.csv", Key: "ahead.csv"},
		{Local: "r/gone.csv", Key: "gone.csv"},
	}}
	// gone.csv is deliberately absent from localMD5 (no local file).
	localMD5 := map[string]string{
		"r/insync.csv": md5hex([]byte("same")),
		"r/behind.csv": md5hex([]byte("old")),
		"r/ahead.csv":  md5hex([]byte("never pushed")),
	}

	got, err := datasetWorkspaceStatus(context.Background(), ws, ds, localMD5)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]workspace.KeyState{
		"insync.csv": workspace.KeyInSync,
		"behind.csv": workspace.KeyBehind,
		"ahead.csv":  workspace.KeyNotPushed,
		"gone.csv":   workspace.KeyMissing,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d key statuses, want %d: %+v", len(got), len(want), got)
	}
	for _, ks := range got {
		if want[ks.Key] != ks.State {
			t.Errorf("%s = %q, want %q", ks.Key, ks.State, want[ks.Key])
		}
	}
	// The journal head's seq is reported so the detail can name a version.
	for _, ks := range got {
		if ks.Key == "behind.csv" && ks.HeadSeq != 2 {
			t.Errorf("behind.csv HeadSeq = %d, want 2", ks.HeadSeq)
		}
	}
	if got := aggregateWorkspaceState(got); got != wsStateDiverged {
		t.Errorf("aggregate = %q, want %q (one key to push, others to pull)", got, wsStateDiverged)
	}
}

// A key whose local file matches content the workspace has never seen is
// AHEAD; the journal is only consulted for keys that actually drifted.
func TestDatasetWorkspaceStatus_AheadNeedsNoBaseline(t *testing.T) {
	ws := newTestWorkspace(t)
	wsPush(t, ws, "a.csv", []byte("v1"))

	ds := &manifest.Dataset{Slug: "s", Files: []manifest.DatasetFile{{Local: "a.csv", Key: "a.csv"}}}
	got, err := datasetWorkspaceStatus(context.Background(), ws, ds, map[string]string{"a.csv": md5hex([]byte("edited"))})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].State != workspace.KeyAhead {
		t.Fatalf("got %+v, want one AHEAD key", got)
	}
	if aggregateWorkspaceState(got) != wsStateAhead {
		t.Errorf("aggregate = %q, want %q", aggregateWorkspaceState(got), wsStateAhead)
	}
}
