package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// These paths return before any network client is dereferenced, so nil clients
// are safe and let us test the gate decisions in isolation.

func planFor(entry *manifest.Entry, state manifest.FileState, localMD5 string, versions []manifest.RemoteVersion) entryPlan {
	return entryPlan{
		entry: entry, proj: "abc12", localAbs: "/tmp/" + entry.Local, localMD5: localMD5,
		state: state, remoteVersions: versions,
	}
}

func TestExecuteEntry_PinOnly_RecordsPinNoTransfer(t *testing.T) {
	entry := &manifest.Entry{Local: "a.csv", Remote: "/a.csv", Version: 0}
	versions := []manifest.RemoteVersion{{Version: 2, MD5: "aaa"}, {Version: 1, MD5: "bbb"}}
	p := planFor(entry, manifest.StatePinOnly, "aaa", versions)

	action, changed, err := executeEntry(context.Background(), p, actionPin, transferDeps{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed || action != "pinned" {
		t.Errorf("action=%q changed=%v, want pinned/true", action, changed)
	}
	if entry.Version != 2 || entry.MD5 != "aaa" {
		t.Errorf("entry pinned to v%d/%s, want v2/aaa", entry.Version, entry.MD5)
	}
}

// A push that would bury a newer remote version is no longer a hard failure of
// the whole run: it carries no local work (local content is already on the
// remote), so it is simply not selected unless --force says otherwise.
func TestPushDecision_RollbackNeedsForce(t *testing.T) {
	if got := pushDecision(manifest.StateRemoteNewer, true, false, ""); got != actionNone {
		t.Errorf("without --force = %v, want actionNone (skipped, not refused)", got)
	}
	if got := pushDecision(manifest.StateRemoteNewer, true, true, ""); got != actionPush {
		t.Errorf("with --force = %v, want actionPush", got)
	}
}

func TestExecuteEntry_DivergedFailsHard(t *testing.T) {
	entry := &manifest.Entry{Local: "a.csv", Remote: "/a.csv", Version: 1, MD5: "B"}
	versions := []manifest.RemoteVersion{{Version: 2, MD5: "R"}, {Version: 1, MD5: "B"}}
	p := planFor(entry, manifest.StateDivergent, "L", versions)

	_, changed, err := executeEntry(context.Background(), p, actionBlocked, transferDeps{})
	if err == nil || !strings.Contains(err.Error(), "divergence") {
		t.Fatalf("expected a divergence error, got %v", err)
	}
	if changed {
		t.Error("manifest must not change on a blocked entry")
	}
}

// AHEAD_OF_MANIFEST is ambiguous, so sync reports it and transfers nothing —
// in either direction. The manifest is left untouched.
func TestExecuteEntry_AheadIsReportedNotTransferred(t *testing.T) {
	entry := &manifest.Entry{Local: "a.csv", Remote: "/a.csv", Version: 1, MD5: "B"}
	versions := []manifest.RemoteVersion{{Version: 1, MD5: "B"}}
	p := planFor(entry, manifest.StateAheadOfManifest, "L", versions)

	action, changed, err := executeEntry(context.Background(), p, actionReport, transferDeps{})
	if err != nil {
		t.Fatalf("reporting must not be an error: %v", err)
	}
	if changed {
		t.Error("manifest must not change when an entry is only reported")
	}
	if action != "skipped_modified" {
		t.Errorf("action = %q, want skipped_modified", action)
	}
	if entry.Version != 1 || entry.MD5 != "B" {
		t.Errorf("pin was mutated: %+v", entry)
	}
}

// The divergence message must only suggest resolutions the commands actually
// honor — previously it advertised `--resolve` combinations sync refused.
func TestDivergenceError_SuggestsResolutionsThatWork(t *testing.T) {
	entry := manifest.Entry{Local: "a.csv", Remote: "/a.csv", Version: 1, MD5: "B"}
	versions := []manifest.RemoteVersion{{Version: 2, MD5: "R"}, {Version: 1, MD5: "B"}}
	msg := divergenceError(entry, "abc12", "L", versions).Error()

	for _, want := range []string{"gosf sync --resolve=theirs", "gosf sync --resolve=ours"} {
		if !strings.Contains(msg, want) {
			t.Errorf("divergence message should suggest %q:\n%s", want, msg)
		}
	}
	// Both suggestions must be honored by the very command they name.
	if got := syncDecision(manifest.StateDivergent, true, false, "theirs"); got == actionBlocked {
		t.Error("sync --resolve=theirs is suggested but still blocked")
	}
	if got := syncDecision(manifest.StateDivergent, true, false, "ours"); got == actionBlocked {
		t.Error("sync --resolve=ours is suggested but still blocked")
	}
}
