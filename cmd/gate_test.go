package cmd

import (
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

func TestLatestRemoteVersionInfo(t *testing.T) {
	versions := []manifest.RemoteVersion{{Version: 1, MD5: "a"}, {Version: 3, MD5: "c"}, {Version: 2, MD5: "b"}}
	got := latestRemoteVersionInfo(versions)
	if got.Version != 3 || got.MD5 != "c" {
		t.Errorf("latestRemoteVersionInfo = %+v, want {3 c}", got)
	}
	if z := latestRemoteVersionInfo(nil); z.Version != 0 || z.MD5 != "" {
		t.Errorf("latestRemoteVersionInfo(nil) = %+v, want zero", z)
	}
}

func TestPushActionLabel(t *testing.T) {
	cases := map[manifest.FileState]string{
		manifest.StateNotPushed:       "new",
		manifest.StateAheadOfManifest: "update",
		manifest.StatePinOnly:         "pin",
		manifest.StateInSync:          "unchanged",
		manifest.StateRemoteNewer:     "rollback",
		manifest.StateBehind:          "rollback",
		manifest.StateDivergent:       "diverged",
		manifest.StateMissing:         "missing",
	}
	for state, want := range cases {
		if got := pushActionLabel(state); got != want {
			t.Errorf("pushActionLabel(%v) = %q, want %q", state, got, want)
		}
	}
}

func TestNeedsPushConfirmation(t *testing.T) {
	if needsPushConfirmation([]manifest.FileState{manifest.StateInSync, manifest.StatePinOnly}) {
		t.Error("pins/unchanged should not need confirmation")
	}
	if !needsPushConfirmation([]manifest.FileState{manifest.StateInSync, manifest.StateNotPushed}) {
		t.Error("a new file should need confirmation")
	}
	if !needsPushConfirmation([]manifest.FileState{manifest.StateAheadOfManifest}) {
		t.Error("an update should need confirmation")
	}
}

func TestSummarizePush(t *testing.T) {
	states := []manifest.FileState{
		manifest.StateNotPushed, manifest.StateNotPushed, manifest.StateNotPushed,
		manifest.StateAheadOfManifest, manifest.StateAheadOfManifest,
		manifest.StatePinOnly, manifest.StateInSync, manifest.StatePinOnly,
	}
	got := summarizePush(states)
	want := "3 new, 2 updated, 3 unchanged (skipped)"
	if got != want {
		t.Errorf("summarizePush = %q, want %q", got, want)
	}
}

func TestDivergenceError(t *testing.T) {
	entry := manifest.Entry{Local: "data/species.csv", Remote: "/data/species.csv", Version: 1, MD5: "X"}
	versions := []manifest.RemoteVersion{{Version: 2, MD5: "Y"}, {Version: 1, MD5: "X"}}
	err := divergenceError(entry, "z2qm3", "Z", versions)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	// Names the file and all three states.
	for _, want := range []string{"species.csv", "v1", "v2", "X", "Y", "Z", "divergence"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q:\n%s", want, msg)
		}
	}
	// Gives both resolution commands.
	if !strings.Contains(msg, "--resolve=theirs") || !strings.Contains(msg, "--resolve=ours") {
		t.Errorf("diagnostic must offer both --resolve options:\n%s", msg)
	}
	// References the remote target so pull is copy-pasteable.
	if !strings.Contains(msg, "z2qm3:/data/species.csv") {
		t.Errorf("diagnostic must name the remote target:\n%s", msg)
	}
}

func TestDivergenceError_EmptyBaselineMD5(t *testing.T) {
	entry := manifest.Entry{Local: "x.csv", Remote: "/x.csv", Version: 0, MD5: ""}
	err := divergenceError(entry, "abc12", "Z", []manifest.RemoteVersion{{Version: 1, MD5: "Y"}})
	if !strings.Contains(err.Error(), "(none)") {
		t.Errorf("empty baseline md5 should render as (none):\n%s", err.Error())
	}
}
