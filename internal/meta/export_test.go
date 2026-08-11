package meta_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/meta"
)

func exportDataset() *manifest.Dataset {
	return &manifest.Dataset{
		Slug:       "counts",
		Record:     "585301",
		Version:    2,
		VersionDOI: "10.5072/zenodo.585301",
		ConceptDOI: "10.5072/zenodo.585299",
		Metadata:   validMeta(),
		Files: []manifest.DatasetFile{
			{Local: "results/counts.h5", Key: "counts.h5", MD5: "aabbcc"},
			{Local: "results/coldata.csv", Key: "coldata.csv", MD5: "ddeeff"},
		},
	}
}

func TestDatapackage(t *testing.T) {
	data, err := meta.Datapackage(exportDataset(), map[string]int64{
		"results/counts.h5": 1234, "results/coldata.csv": 56,
	})
	if err != nil {
		t.Fatalf("Datapackage: %v", err)
	}
	var dp struct {
		Name     string `json:"name"`
		ID       string `json:"id"`
		Title    string `json:"title"`
		Licenses []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"licenses"`
		Resources []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			Bytes int64  `json:"bytes"`
			Hash  string `json:"hash"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(data, &dp); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, data)
	}
	if dp.Name != "counts" || dp.Title != "Aligned counts" {
		t.Errorf("name/title = %q/%q", dp.Name, dp.Title)
	}
	if dp.ID != "https://doi.org/10.5072/zenodo.585301" {
		t.Errorf("id = %q, want the version DOI URL", dp.ID)
	}
	if len(dp.Licenses) != 1 || dp.Licenses[0].Name != "CC0-1.0" {
		t.Errorf("licenses = %+v", dp.Licenses)
	}
	if len(dp.Resources) != 2 {
		t.Fatalf("resources = %+v", dp.Resources)
	}
	if dp.Resources[0].Path != "counts.h5" || dp.Resources[0].Bytes != 1234 || dp.Resources[0].Hash != "md5:aabbcc" {
		t.Errorf("resource[0] = %+v", dp.Resources[0])
	}
	// Data Package resource names must be lowercase alnum with ._- only.
	for _, r := range dp.Resources {
		if r.Name != strings.ToLower(r.Name) || strings.ContainsAny(r.Name, " /") {
			t.Errorf("resource name %q violates the Data Package name rules", r.Name)
		}
	}
}

func TestROCrate(t *testing.T) {
	data, err := meta.ROCrate(exportDataset(), map[string]int64{"results/counts.h5": 1234})
	if err != nil {
		t.Fatalf("ROCrate: %v", err)
	}
	var crate struct {
		Context string           `json:"@context"`
		Graph   []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(data, &crate); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if !strings.Contains(crate.Context, "w3id.org/ro/crate") {
		t.Errorf("@context = %q", crate.Context)
	}
	byID := map[string]map[string]any{}
	for _, e := range crate.Graph {
		if id, ok := e["@id"].(string); ok {
			byID[id] = e
		}
	}
	root, ok := byID["./"]
	if !ok {
		t.Fatal("no root dataset entity ./")
	}
	if root["name"] != "Aligned counts" {
		t.Errorf("root name = %v", root["name"])
	}
	if root["identifier"] != "https://doi.org/10.5072/zenodo.585301" {
		t.Errorf("root identifier = %v", root["identifier"])
	}
	if _, ok := byID["counts.h5"]; !ok {
		t.Error("file entity counts.h5 missing")
	}
	if _, ok := byID["https://orcid.org/0000-0002-1825-0097"]; !ok {
		t.Error("creator Person entity keyed by ORCID URL missing")
	}
	// The metadata descriptor must be present and point at the root.
	desc, ok := byID["ro-crate-metadata.json"]
	if !ok {
		t.Fatal("metadata descriptor entity missing")
	}
	about, _ := desc["about"].(map[string]any)
	if about["@id"] != "./" {
		t.Errorf("descriptor about = %v", desc["about"])
	}
}
