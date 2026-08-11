package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/manifest"
)

func samplePlans() ([]entryPlan, []manifest.FileState) {
	plans := []entryPlan{
		{entry: &manifest.Entry{Local: "a.csv", Remote: "/a.csv"}, proj: "z2qm3", localAbs: "/no/such/a.csv", localMD5: "aaa"},
		{entry: &manifest.Entry{Local: "b.csv", Remote: "/b.csv", Version: 1, MD5: "old"}, proj: "z2qm3", localAbs: "/no/such/b.csv", localMD5: "newmd5"},
	}
	states := []manifest.FileState{manifest.StateNotPushed, manifest.StateAheadOfManifest}
	return plans, states
}

func TestPrintPushPlan_PublicWarningAndSummary(t *testing.T) {
	node := &client.Node{ID: "z2qm3"}
	node.Attributes.Title = "My Study"
	node.Attributes.Public = true
	plans, states := samplePlans()

	var buf bytes.Buffer
	printPushPlan(&buf, node, "z2qm3", plans, states)
	out := buf.String()

	for _, want := range []string{"My Study", "z2qm3", "PUBLIC", "WARNING", "new", "update", "a.csv", "b.csv", "Summary", "1 new, 1 updated"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintPushPlan_PrivateNoWarning(t *testing.T) {
	node := &client.Node{ID: "z2qm3"}
	node.Attributes.Title = "Private Study"
	node.Attributes.Public = false
	plans, states := samplePlans()

	var buf bytes.Buffer
	printPushPlan(&buf, node, "z2qm3", plans, states)
	out := buf.String()

	if !strings.Contains(out, "PRIVATE") {
		t.Errorf("expected PRIVATE marker:\n%s", out)
	}
	if strings.Contains(out, "WARNING") {
		t.Errorf("private project must not print the public warning:\n%s", out)
	}
}

func TestPrintPushPlan_NilNodeFallsBackToGUID(t *testing.T) {
	plans, states := samplePlans()
	var buf bytes.Buffer
	printPushPlan(&buf, nil, "z2qm3", plans, states)
	out := buf.String()
	if !strings.Contains(out, "z2qm3") {
		t.Errorf("nil node should fall back to the project GUID:\n%s", out)
	}
}
