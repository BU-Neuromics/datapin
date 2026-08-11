package manifest_test

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// makeEntry builds a minimal Entry for testing ClassifyFile. There is no
// direction to pass: classification is a pure function of local content, the
// pinned baseline, and the remote versions (issue #81).
func makeEntry(version int, md5 string) manifest.Entry {
	return manifest.Entry{
		Local:   "data/file.csv",
		Remote:  "/data/file.csv",
		Version: version,
		MD5:     md5,
	}
}

// rv is a convenience constructor for RemoteVersion.
func rv(version int, md5 string) manifest.RemoteVersion {
	return manifest.RemoteVersion{Version: version, MD5: md5}
}

func TestClassifyFile_InSync(t *testing.T) {
	entry := makeEntry(3, "aaa")
	versions := []manifest.RemoteVersion{
		rv(1, "ccc"), rv(2, "bbb"), rv(3, "aaa"),
	}
	state := manifest.ClassifyFile(entry, "aaa", versions, false)
	if state != manifest.StateInSync {
		t.Errorf("state = %v, want IN_SYNC", state)
	}
}

func TestClassifyFile_Missing(t *testing.T) {
	entry := makeEntry(3, "aaa")
	state := manifest.ClassifyFile(entry, "", nil, false)
	if state != manifest.StateMissing {
		t.Errorf("state = %v, want MISSING", state)
	}
}

func TestClassifyFile_Missing_NoRemote(t *testing.T) {
	entry := makeEntry(3, "aaa")
	state := manifest.ClassifyFile(entry, "", nil, true)
	if state != manifest.StateMissing {
		t.Errorf("state = %v, want MISSING", state)
	}
}

func TestClassifyFile_Behind(t *testing.T) {
	// Local MD5 matches v2, but declared version is v3.
	entry := makeEntry(3, "aaa")
	versions := []manifest.RemoteVersion{
		rv(1, "ccc"), rv(2, "bbb"), rv(3, "aaa"),
	}
	state := manifest.ClassifyFile(entry, "bbb", versions, false)
	if state != manifest.StateBehind {
		t.Errorf("state = %v, want BEHIND", state)
	}
}

func TestClassifyFile_AheadOfManifest(t *testing.T) {
	// Local MD5 doesn't match any remote version.
	entry := makeEntry(3, "aaa")
	versions := []manifest.RemoteVersion{
		rv(1, "ccc"), rv(2, "bbb"), rv(3, "aaa"),
	}
	state := manifest.ClassifyFile(entry, "zzz", versions, false)
	if state != manifest.StateAheadOfManifest {
		t.Errorf("state = %v, want AHEAD_OF_MANIFEST", state)
	}
}

func TestClassifyFile_RemoteNewer(t *testing.T) {
	// Local MD5 matches declared version v3, but remote has v4 and v5.
	entry := makeEntry(3, "aaa")
	versions := []manifest.RemoteVersion{
		rv(1, "ccc"), rv(2, "bbb"), rv(3, "aaa"), rv(4, "ddd"), rv(5, "eee"),
	}
	state := manifest.ClassifyFile(entry, "aaa", versions, false)
	if state != manifest.StateRemoteNewer {
		t.Errorf("state = %v, want REMOTE_NEWER", state)
	}
}

func TestClassifyFile_NotPushed(t *testing.T) {
	entry := makeEntry(0, "")
	state := manifest.ClassifyFile(entry, "anything", nil, false)
	if state != manifest.StateNotPushed {
		t.Errorf("state = %v, want NOT_PUSHED", state)
	}
}

func TestClassifyFile_NotPushed_LocalMissing(t *testing.T) {
	entry := makeEntry(0, "")
	state := manifest.ClassifyFile(entry, "", nil, false)
	if state != manifest.StateNotPushed {
		t.Errorf("state = %v, want NOT_PUSHED (even when local is missing)", state)
	}
}

// ---- no-check-remote tests ----

func TestClassifyFile_NoCheckRemote_InSync(t *testing.T) {
	entry := makeEntry(3, "aaa")
	state := manifest.ClassifyFile(entry, "aaa", nil, true)
	if state != manifest.StateInSync {
		t.Errorf("state = %v, want IN_SYNC", state)
	}
}

func TestClassifyFile_NoCheckRemote_AheadOfManifest(t *testing.T) {
	entry := makeEntry(3, "aaa")
	state := manifest.ClassifyFile(entry, "zzz", nil, true)
	if state != manifest.StateAheadOfManifest {
		t.Errorf("state = %v, want AHEAD_OF_MANIFEST", state)
	}
}

func TestClassifyFile_NoCheckRemote_NotPushed(t *testing.T) {
	entry := makeEntry(0, "")
	state := manifest.ClassifyFile(entry, "zzz", nil, true)
	if state != manifest.StateNotPushed {
		t.Errorf("state = %v, want NOT_PUSHED", state)
	}
}

// ---- edge cases ----

func TestClassifyFile_DeclaredVersionHighestRemote_InSync(t *testing.T) {
	// Local matches the declared version AND it is the highest remote version.
	entry := makeEntry(3, "aaa")
	versions := []manifest.RemoteVersion{
		rv(1, "ccc"), rv(2, "bbb"), rv(3, "aaa"),
	}
	state := manifest.ClassifyFile(entry, "aaa", versions, false)
	if state != manifest.StateInSync {
		t.Errorf("state = %v, want IN_SYNC", state)
	}
}

func TestClassifyFile_LocalMatchesOlderAndNewer(t *testing.T) {
	// Edge: local MD5 happens to match v1 (behind), but declared version is v3.
	entry := makeEntry(3, "aaa")
	versions := []manifest.RemoteVersion{
		rv(1, "bbb"), rv(2, "ccc"), rv(3, "aaa"),
	}
	state := manifest.ClassifyFile(entry, "bbb", versions, false)
	if state != manifest.StateBehind {
		t.Errorf("state = %v, want BEHIND", state)
	}
}

func TestClassifyFile_EmptyRemoteVersions_AheadOfManifest(t *testing.T) {
	// Version > 0 but remote returned empty list (unusual but should not panic).
	entry := makeEntry(2, "aaa")
	state := manifest.ClassifyFile(entry, "bbb", []manifest.RemoteVersion{}, false)
	if state != manifest.StateAheadOfManifest {
		t.Errorf("state = %v, want AHEAD_OF_MANIFEST", state)
	}
}

// ---- content-aware classification of unpinned entries (epic #38) ----

func TestClassifyFile_Unpinned_LocalMatchesRemoteLatest_PinOnly(t *testing.T) {
	// The keystone case: entry added at version=0, local bytes identical to the
	// current remote version. No transfer needed — record the pin.
	entry := makeEntry(0, "")
	versions := []manifest.RemoteVersion{rv(2, "aaa"), rv(1, "bbb")}
	state := manifest.ClassifyFile(entry, "aaa", versions, false)
	if state != manifest.StatePinOnly {
		t.Errorf("state = %v, want PIN_ONLY", state)
	}
}

func TestClassifyFile_Unpinned_LocalMatchesOlderRemote_Behind(t *testing.T) {
	entry := makeEntry(0, "")
	versions := []manifest.RemoteVersion{rv(2, "aaa"), rv(1, "bbb")}
	state := manifest.ClassifyFile(entry, "bbb", versions, false)
	if state != manifest.StateBehind {
		t.Errorf("state = %v, want BEHIND", state)
	}
}

func TestClassifyFile_Unpinned_LocalMatchesNothing_Ahead(t *testing.T) {
	entry := makeEntry(0, "")
	versions := []manifest.RemoteVersion{rv(2, "aaa"), rv(1, "bbb")}
	state := manifest.ClassifyFile(entry, "zzz", versions, false)
	if state != manifest.StateAheadOfManifest {
		t.Errorf("state = %v, want AHEAD_OF_MANIFEST", state)
	}
}

func TestClassifyFile_Unpinned_NoRemote_NotPushed(t *testing.T) {
	entry := makeEntry(0, "")
	state := manifest.ClassifyFile(entry, "aaa", nil, false)
	if state != manifest.StateNotPushed {
		t.Errorf("state = %v, want NOT_PUSHED", state)
	}
}

func TestClassifyFile_Unpinned_LocalMissingRemoteExists_Missing(t *testing.T) {
	entry := makeEntry(0, "")
	versions := []manifest.RemoteVersion{rv(1, "aaa")}
	state := manifest.ClassifyFile(entry, "", versions, false)
	if state != manifest.StateMissing {
		t.Errorf("state = %v, want MISSING", state)
	}
}

// ---- divergence detection (epic #38) ----

func TestClassifyFile_Divergent(t *testing.T) {
	// Baseline v1/aaa. Local changed to zzz. Remote advanced to v2/yyy.
	// Neither side matches the other or the baseline → unsafe.
	entry := makeEntry(1, "aaa")
	versions := []manifest.RemoteVersion{rv(2, "yyy"), rv(1, "aaa")}
	state := manifest.ClassifyFile(entry, "zzz", versions, false)
	if state != manifest.StateDivergent {
		t.Errorf("state = %v, want DIVERGED", state)
	}
}

func TestClassifyFile_PinnedLocalMatchesNewRemote_PinOnly(t *testing.T) {
	// Baseline v1/aaa. Remote advanced to v2/yyy AND local now equals yyy
	// (e.g. someone pushed our exact content). Convergent → just re-pin.
	entry := makeEntry(1, "aaa")
	versions := []manifest.RemoteVersion{rv(2, "yyy"), rv(1, "aaa")}
	state := manifest.ClassifyFile(entry, "yyy", versions, false)
	if state != manifest.StatePinOnly {
		t.Errorf("state = %v, want PIN_ONLY", state)
	}
}

func TestClassifyFile_PinnedLocalMatchesOlderRemoteBothMoved_Behind(t *testing.T) {
	// Baseline v2/bbb. Remote advanced to v3/ccc. Local rolled back to v1/aaa
	// (a known remote version) → BEHIND, not divergence (no unique local work).
	entry := makeEntry(2, "bbb")
	versions := []manifest.RemoteVersion{rv(3, "ccc"), rv(2, "bbb"), rv(1, "aaa")}
	state := manifest.ClassifyFile(entry, "aaa", versions, false)
	if state != manifest.StateBehind {
		t.Errorf("state = %v, want BEHIND", state)
	}
}

func TestFileState_String(t *testing.T) {
	cases := []struct {
		state manifest.FileState
		want  string
	}{
		{manifest.StateInSync, "IN_SYNC"},
		{manifest.StateMissing, "MISSING"},
		{manifest.StateBehind, "BEHIND"},
		{manifest.StateAheadOfManifest, "AHEAD_OF_MANIFEST"},
		{manifest.StateRemoteNewer, "REMOTE_NEWER"},
		{manifest.StateNotPushed, "NOT_PUSHED"},
		{manifest.StatePinOnly, "PIN_ONLY"},
		{manifest.StateDivergent, "DIVERGED"},
	}
	for _, tc := range cases {
		if tc.state.String() != tc.want {
			t.Errorf("FileState(%v).String() = %q, want %q", tc.state, tc.state.String(), tc.want)
		}
	}
}
