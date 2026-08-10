package cmd

import (
	"reflect"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/gitutil"
	"github.com/BU-Neuromics/datapin/internal/manifest"
)

func TestRemotePath(t *testing.T) {
	cases := []struct {
		base, rel, want string
	}{
		{"/", "data/x.csv", "/data/x.csv"},
		{"", "data/x.csv", "/data/x.csv"},
		{"/inputs", "data/x.csv", "/inputs/data/x.csv"},
		{"inputs/", "x.csv", "/inputs/x.csv"},
		{"/inputs/", "/x.csv", "/inputs/x.csv"},
	}
	for _, tc := range cases {
		if got := remotePath(tc.base, tc.rel); got != tc.want {
			t.Errorf("remotePath(%q,%q) = %q, want %q", tc.base, tc.rel, got, tc.want)
		}
	}
}

func TestUntrackedCandidates(t *testing.T) {
	m := &manifest.Manifest{Files: []manifest.Entry{
		{Local: "data/tracked.csv"},
		{Local: "notes.txt"},
	}}
	cands := []gitutil.Candidate{
		{Path: "data/tracked.csv"},
		{Path: "data/new.csv"},
		{Path: "notes.txt"},
		{Path: "fresh.bin"},
	}
	got := untrackedCandidates(cands, m)
	var paths []string
	for _, c := range got {
		paths = append(paths, c.Path)
	}
	if !reflect.DeepEqual(paths, []string{"data/new.csv", "fresh.bin"}) {
		t.Errorf("untrackedCandidates = %v, want [data/new.csv fresh.bin]", paths)
	}
}
