package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

func publishReadyDataset() *manifest.Dataset {
	return &manifest.Dataset{
		Slug: "counts",
		Metadata: manifest.DatasetMetadata{
			Title:   "Aligned counts",
			License: "CC0-1.0",
			Creators: []manifest.DatasetCreator{
				{Name: "Tester, Trusty"},
			},
		},
		Files: []manifest.DatasetFile{{Local: "results/counts.tsv"}},
	}
}

// Publishing grants rights permanently; the license must be an explicit
// user choice, never an omission the backend fills in with its own
// default (D37, revising D20). check keeps this a warning — the publish
// boundary makes it an error.
func TestPublishPreflight_RequiresLicense(t *testing.T) {
	ds := publishReadyDataset()
	ds.Metadata.License = ""
	err := publishPreflight(ds)
	if err == nil {
		t.Fatal("publishPreflight allowed a dataset with no license")
	}
	if !strings.Contains(err.Error(), "license") {
		t.Fatalf("error %q should explain the missing license", err)
	}

	ds.Metadata.License = "CC0-1.0"
	if err := publishPreflight(ds); err != nil {
		t.Fatalf("publishPreflight with license: %v", err)
	}
}

// The confirmation plan shows the license next to the PUBLIC/PERMANENT
// warning — the choice must be visible at the moment of consent.
func TestPrintPublishPlan_ShowsLicense(t *testing.T) {
	var buf bytes.Buffer
	printPublishPlan(&buf, publishReadyDataset(), "sandbox", "https://sandbox.zenodo.org", nil, nil, false, 0, false)
	if !strings.Contains(buf.String(), "license: CC0-1.0") {
		t.Fatalf("plan output missing the license line:\n%s", buf.String())
	}
}
