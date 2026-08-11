package cmd

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

func TestPlanDataset(t *testing.T) {
	files := []manifest.DatasetFile{
		{Local: "results/a.csv", Key: "a.csv", MD5: "aaa"}, // unchanged
		{Local: "results/b.csv", Key: "b.csv", MD5: "bbb"}, // changed locally
		{Local: "results/c.csv", Key: "c.csv", MD5: ""},    // new, never published
	}
	localMD5 := map[string]string{
		"results/a.csv": "aaa",
		"results/b.csv": "bbb2",
		"results/c.csv": "ccc",
	}
	remote := map[string]string{
		"a.csv":       "aaa",
		"b.csv":       "bbb",
		"stale.chart": "sss", // on the remote, no longer in the manifest
	}

	plan := planDataset(files, localMD5, remote)

	want := map[string]datasetAction{
		"a.csv":       actionKeep,
		"b.csv":       actionReplace,
		"c.csv":       actionUpload,
		"stale.chart": actionRemove,
	}
	if len(plan) != len(want) {
		t.Fatalf("plan has %d entries, want %d: %+v", len(plan), len(want), plan)
	}
	for _, e := range plan {
		if want[e.Key] != e.Action {
			t.Errorf("plan[%s] = %s, want %s", e.Key, e.Action, want[e.Key])
		}
	}
}

func TestPlanDataset_FirstPublishIsAllUploads(t *testing.T) {
	files := []manifest.DatasetFile{
		{Local: "a.csv", Key: "a.csv", MD5: ""},
		{Local: "b.csv", Key: "b.csv", MD5: ""},
	}
	localMD5 := map[string]string{"a.csv": "x", "b.csv": "y"}

	plan := planDataset(files, localMD5, nil)
	for _, e := range plan {
		if e.Action != actionUpload {
			t.Errorf("first publish: plan[%s] = %s, want upload", e.Key, e.Action)
		}
	}
}

func TestPlanDataset_IdenticalContentIsNoWork(t *testing.T) {
	files := []manifest.DatasetFile{{Local: "a.csv", Key: "a.csv", MD5: "aaa"}}
	localMD5 := map[string]string{"a.csv": "aaa"}
	remote := map[string]string{"a.csv": "aaa"}

	plan := planDataset(files, localMD5, remote)
	if planHasWork(plan) {
		t.Errorf("identical local/remote must plan no work: %+v", plan)
	}
}

func TestPlanDataset_LocalMatchesRemoteButNotPin(t *testing.T) {
	// The pin is stale but content already matches the remote — keep, and
	// the caller re-pins without a transfer (the PIN_ONLY analogue).
	files := []manifest.DatasetFile{{Local: "a.csv", Key: "a.csv", MD5: "old"}}
	localMD5 := map[string]string{"a.csv": "aaa"}
	remote := map[string]string{"a.csv": "aaa"}

	plan := planDataset(files, localMD5, remote)
	if planHasWork(plan) {
		t.Errorf("local == remote-latest must plan no transfer: %+v", plan)
	}
}

func TestDatasetState(t *testing.T) {
	tests := []struct {
		name                      string
		published                 bool
		localChanged, remoteNewer bool
		localMissing              bool
		want                      string
	}{
		{"never published", false, false, false, false, "NOT_PUBLISHED"},
		{"clean", true, false, false, false, "IN_SYNC"},
		{"local edits", true, true, false, false, "AHEAD"},
		{"remote moved", true, false, true, false, "REMOTE_NEWER"},
		{"both moved", true, true, true, false, "DIVERGED"},
		{"local file gone", true, false, false, true, "MISSING"},
		{"gone and remote moved", true, false, true, true, "MISSING"},
	}
	for _, tt := range tests {
		got := datasetState(tt.published, tt.localChanged, tt.remoteNewer, tt.localMissing)
		if got != tt.want {
			t.Errorf("%s: datasetState = %q, want %q", tt.name, got, tt.want)
		}
	}
}
