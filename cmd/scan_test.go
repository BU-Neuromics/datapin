package cmd

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// The skip is only valid when the latest-version fast path classifies a file
// identically to having the full version history. This test proves that
// equivalence: for every case where canSkipVersionHistory says yes, feeding
// ClassifyFile the synthetic latest-only slice must match feeding it the full
// history.
func TestCanSkipVersionHistory_EquivalentToFullHistory(t *testing.T) {
	type scenario struct {
		name     string
		entry    manifest.Entry
		local    string
		full     []manifest.RemoteVersion // newest-first
		wantSkip bool
	}
	scenarios := []scenario{
		{
			name:     "in sync: local=baseline=latest",
			entry:    manifest.Entry{Version: 3, MD5: "c"},
			local:    "c",
			full:     []manifest.RemoteVersion{{Version: 3, MD5: "c"}, {Version: 2, MD5: "b"}, {Version: 1, MD5: "a"}},
			wantSkip: true,
		},
		{
			name:     "remote newer, identical bytes new version number",
			entry:    manifest.Entry{Version: 2, MD5: "b"},
			local:    "b",
			full:     []manifest.RemoteVersion{{Version: 3, MD5: "b"}, {Version: 2, MD5: "b"}, {Version: 1, MD5: "a"}},
			wantSkip: true, // local==baseline
		},
		{
			name:     "remote newer, different bytes; local still baseline",
			entry:    manifest.Entry{Version: 2, MD5: "b"},
			local:    "b",
			full:     []manifest.RemoteVersion{{Version: 3, MD5: "c"}, {Version: 2, MD5: "b"}, {Version: 1, MD5: "a"}},
			wantSkip: true, // local==baseline → REMOTE_NEWER by number alone
		},
		{
			name:     "pin only: local equals remote latest but not baseline",
			entry:    manifest.Entry{Version: 1, MD5: "a"},
			local:    "c",
			full:     []manifest.RemoteVersion{{Version: 3, MD5: "c"}, {Version: 2, MD5: "b"}, {Version: 1, MD5: "a"}},
			wantSkip: true, // local==latest
		},
		{
			name:     "unpinned but local equals remote latest",
			entry:    manifest.Entry{Version: 0, MD5: ""},
			local:    "c",
			full:     []manifest.RemoteVersion{{Version: 3, MD5: "c"}, {Version: 2, MD5: "b"}},
			wantSkip: true, // local==latest
		},
		{
			name:     "BEHIND: local matches an older version, not latest",
			entry:    manifest.Entry{Version: 3, MD5: "c"},
			local:    "b",
			full:     []manifest.RemoteVersion{{Version: 3, MD5: "c"}, {Version: 2, MD5: "b"}, {Version: 1, MD5: "a"}},
			wantSkip: false, // needs history to know "b" is v2
		},
		{
			name:     "DIVERGED: local matches no remote version, both moved",
			entry:    manifest.Entry{Version: 2, MD5: "b"},
			local:    "x",
			full:     []manifest.RemoteVersion{{Version: 3, MD5: "c"}, {Version: 2, MD5: "b"}},
			wantSkip: false,
		},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			latest := latestRemoteVersionSlice(s.full)
			got := canSkipVersionHistory(s.local, latest.MD5, latest.Version, s.entry)
			if got != s.wantSkip {
				t.Fatalf("canSkipVersionHistory = %v, want %v", got, s.wantSkip)
			}
			if !s.wantSkip {
				return
			}
			// Prove equivalence: synthetic latest-only vs full history.
			synthetic := []manifest.RemoteVersion{latest}
			gotState := manifest.ClassifyFile(s.entry, s.local, synthetic, false)
			wantState := manifest.ClassifyFile(s.entry, s.local, s.full, false)
			if gotState != wantState {
				t.Errorf("skip changed classification: synthetic=%s full=%s", gotState, wantState)
			}
		})
	}
}

// latestRemoteVersionSlice returns the highest-numbered version.
func latestRemoteVersionSlice(vs []manifest.RemoteVersion) manifest.RemoteVersion {
	var out manifest.RemoteVersion
	for _, v := range vs {
		if v.Version >= out.Version {
			out = v
		}
	}
	return out
}
