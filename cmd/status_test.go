package cmd

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

func TestStateDisplay_NewStates(t *testing.T) {
	versions := []manifest.RemoteVersion{{Version: 2, MD5: "aaa"}, {Version: 1, MD5: "bbb"}}

	t.Run("pin_only reports content match, not never-pushed", func(t *testing.T) {
		entry := manifest.Entry{Local: "data/x.csv", Version: 0}
		status, detail := stateDisplay(manifest.StatePinOnly, entry, versions)
		if status == "·" {
			t.Errorf("PIN_ONLY should not render as the never-pushed marker")
		}
		if !contains(detail, "sync") {
			t.Errorf("PIN_ONLY detail should tell the user to run sync, got %q", detail)
		}
	})

	t.Run("diverged is loud and names both sides", func(t *testing.T) {
		entry := manifest.Entry{Local: "data/x.csv", Version: 1, MD5: "ccc"}
		status, detail := stateDisplay(manifest.StateDivergent, entry, versions)
		if status != "DIVERGED" {
			t.Errorf("status = %q, want DIVERGED", status)
		}
		if detail == "" {
			t.Errorf("DIVERGED should carry an explanatory detail")
		}
	})
}

func TestStatusStateCountsForExit(t *testing.T) {
	// Only IN_SYNC counts as fully in sync; PIN_ONLY and DIVERGED signal work.
	cases := []struct {
		state  manifest.FileState
		inSync bool
	}{
		{manifest.StateInSync, true},
		{manifest.StatePinOnly, false},
		{manifest.StateDivergent, false},
		{manifest.StateBehind, false},
	}
	for _, tc := range cases {
		if got := statusIsInSync(tc.state); got != tc.inSync {
			t.Errorf("statusIsInSync(%v) = %v, want %v", tc.state, got, tc.inSync)
		}
	}
}
