package cmd

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// syncDecision is the whole of sync's policy: every state has exactly one
// correct action, derived from local/baseline/remote content — never from a
// per-entry default recorded weeks earlier (issue #81).
func TestSyncDecision(t *testing.T) {
	tests := []struct {
		name        string
		state       manifest.FileState
		localExists bool
		force       bool
		resolve     string
		want        syncAction
	}{
		{"in sync", manifest.StateInSync, true, false, "", actionNone},
		{"pin only", manifest.StatePinOnly, true, false, "", actionPin},

		// The bug in #81: a file that exists on the remote but not locally is
		// restored by a plain sync, with no flag of any kind.
		{"missing", manifest.StateMissing, false, false, "", actionPull},
		{"missing, force irrelevant", manifest.StateMissing, false, true, "", actionPull},

		{"behind", manifest.StateBehind, true, false, "", actionPull},
		{"remote newer", manifest.StateRemoteNewer, true, false, "", actionPull},

		{"not pushed with local content", manifest.StateNotPushed, true, false, "", actionPush},
		{"not pushed, nothing anywhere", manifest.StateNotPushed, false, false, "", actionNone},

		// Genuinely ambiguous: reported, never guessed.
		{"ahead", manifest.StateAheadOfManifest, true, false, "", actionReport},
		{"ahead with force discards local", manifest.StateAheadOfManifest, true, true, "", actionRestore},

		// --resolve is honored as given, whatever the entry once declared.
		{"diverged unresolved", manifest.StateDivergent, true, false, "", actionBlocked},
		{"diverged ours", manifest.StateDivergent, true, false, "ours", actionPush},
		{"diverged theirs", manifest.StateDivergent, true, false, "theirs", actionPull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syncDecision(tt.state, tt.localExists, tt.force, tt.resolve)
			if got != tt.want {
				t.Errorf("syncDecision(%v, local=%v, force=%v, resolve=%q) = %v, want %v",
					tt.state, tt.localExists, tt.force, tt.resolve, got, tt.want)
			}
		})
	}
}

// Bare `gosf push` publishes local work. It selects the states where local
// holds something the remote does not, plus the deliberate rollback under
// --force — never by a manifest field.
func TestPushDecision(t *testing.T) {
	tests := []struct {
		name        string
		state       manifest.FileState
		localExists bool
		force       bool
		resolve     string
		want        syncAction
	}{
		{"ahead publishes", manifest.StateAheadOfManifest, true, false, "", actionPush},
		{"not pushed with local content", manifest.StateNotPushed, true, false, "", actionPush},
		{"not pushed, no local file", manifest.StateNotPushed, false, false, "", actionNone},
		{"pin only records the pin", manifest.StatePinOnly, true, false, "", actionPin},
		{"in sync", manifest.StateInSync, true, false, "", actionNone},
		{"missing locally", manifest.StateMissing, false, false, "", actionNone},

		// Local content is already on the remote: pushing is a pure rollback,
		// so it happens only when explicitly authorized.
		{"remote newer skipped", manifest.StateRemoteNewer, true, false, "", actionNone},
		{"remote newer with force", manifest.StateRemoteNewer, true, true, "", actionPush},
		{"behind skipped", manifest.StateBehind, true, false, "", actionNone},
		{"behind with force", manifest.StateBehind, true, true, "", actionPush},

		{"diverged unresolved", manifest.StateDivergent, true, false, "", actionBlocked},
		{"diverged ours", manifest.StateDivergent, true, false, "ours", actionPush},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pushDecision(tt.state, tt.localExists, tt.force, tt.resolve)
			if got != tt.want {
				t.Errorf("pushDecision(%v, local=%v, force=%v, resolve=%q) = %v, want %v",
					tt.state, tt.localExists, tt.force, tt.resolve, got, tt.want)
			}
		})
	}
}

// Bare `gosf pull` never uploads: it fetches the states where the remote holds
// something local does not, and leaves local work alone unless forced.
func TestPullDecision(t *testing.T) {
	tests := []struct {
		name    string
		state   manifest.FileState
		force   bool
		resolve string
		want    syncAction
	}{
		{"missing", manifest.StateMissing, false, "", actionPull},
		{"behind", manifest.StateBehind, false, "", actionPull},
		{"remote newer", manifest.StateRemoteNewer, false, "", actionPull},
		{"pin only", manifest.StatePinOnly, false, "", actionPin},
		{"in sync", manifest.StateInSync, false, "", actionNone},
		{"not pushed", manifest.StateNotPushed, false, "", actionNone},
		{"ahead is left alone", manifest.StateAheadOfManifest, false, "", actionReport},
		{"ahead with force", manifest.StateAheadOfManifest, true, "", actionRestore},
		{"diverged unresolved", manifest.StateDivergent, false, "", actionBlocked},
		{"diverged theirs", manifest.StateDivergent, false, "theirs", actionPull},
		// A pull cannot take "ours" — that would upload.
		{"diverged ours is not a pull", manifest.StateDivergent, false, "ours", actionBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pullDecision(tt.state, tt.force, tt.resolve)
			if got != tt.want {
				t.Errorf("pullDecision(%v, force=%v, resolve=%q) = %v, want %v",
					tt.state, tt.force, tt.resolve, got, tt.want)
			}
		})
	}
}

// A diverged entry blocks a bulk run unless --resolve is given, and the two
// resolutions must be symmetric: neither is privileged by anything on the entry.
func TestSyncDecision_ResolveIsSymmetric(t *testing.T) {
	if got := syncDecision(manifest.StateDivergent, true, false, "ours"); got != actionPush {
		t.Errorf("--resolve=ours = %v, want actionPush", got)
	}
	if got := syncDecision(manifest.StateDivergent, true, false, "theirs"); got != actionPull {
		t.Errorf("--resolve=theirs = %v, want actionPull", got)
	}
}
