package meta_test

import (
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/meta"
)

func validMeta() manifest.DatasetMetadata {
	return manifest.DatasetMetadata{
		Title:       "Aligned counts",
		Description: "RNA-seq count matrices, batches 1-4",
		License:     "CC0-1.0",
		Keywords:    []string{"RNA-seq"},
		Creators: []manifest.DatasetCreator{
			{Name: "Labadorf, Adam", ORCID: "0000-0002-1825-0097"}, // valid checksum
		},
		Related: []manifest.RelatedID{
			{Identifier: "10.1101/2026.01.01.123456", Relation: "IsSupplementTo"},
		},
	}
}

func hasError(issues []meta.Issue, field string) bool {
	for _, i := range issues {
		if i.Field == field && i.Severity == meta.Error {
			return true
		}
	}
	return false
}

func hasWarning(issues []meta.Issue, field string) bool {
	for _, i := range issues {
		if i.Field == field && i.Severity == meta.Warning {
			return true
		}
	}
	return false
}

func TestCheck_ValidMetadataIsClean(t *testing.T) {
	issues := meta.Check(validMeta())
	for _, i := range issues {
		if i.Severity == meta.Error {
			t.Errorf("unexpected error on valid metadata: %+v", i)
		}
	}
}

func TestCheck_DataCiteFloor(t *testing.T) {
	m := validMeta()
	m.Title = ""
	m.Creators = nil
	issues := meta.Check(m)
	if !hasError(issues, "title") {
		t.Error("missing title must be an error")
	}
	if !hasError(issues, "creators") {
		t.Error("missing creators must be an error")
	}
}

func TestCheck_ORCID(t *testing.T) {
	m := validMeta()
	m.Creators[0].ORCID = "0000-0002-1825-0098" // bad checksum digit
	if !hasError(meta.Check(m), "creators[0].orcid") {
		t.Error("ORCID with a wrong checksum must be an error")
	}

	m.Creators[0].ORCID = "not-an-orcid"
	if !hasError(meta.Check(m), "creators[0].orcid") {
		t.Error("malformed ORCID must be an error")
	}

	m.Creators[0].ORCID = "https://orcid.org/0000-0002-1825-0097"
	issues := meta.Check(m)
	if hasError(issues, "creators[0].orcid") {
		t.Error("URL-form ORCID must be accepted (normalized)")
	}

	m.Creators[0].ORCID = ""
	m2 := meta.Check(m)
	if hasError(m2, "creators[0].orcid") {
		t.Error("ORCID is optional")
	}
	if !hasWarning(m2, "creators[0].orcid") {
		t.Error("a creator without an ORCID should warn (FAIR nudge)")
	}
}

func TestCheck_License(t *testing.T) {
	m := validMeta()
	m.License = "CC0" // not an SPDX id
	issues := meta.Check(m)
	if !hasError(issues, "license") {
		t.Error("non-SPDX license must be an error")
	}
	for _, i := range issues {
		if i.Field == "license" && !strings.Contains(i.Message, "CC0-1.0") {
			t.Errorf("license error should suggest the near-miss SPDX id, got: %s", i.Message)
		}
	}

	m.License = ""
	if !hasWarning(meta.Check(m), "license") {
		t.Error("missing license should warn")
	}

	m.License = "CC-BY-NC-4.0"
	found := false
	for _, i := range meta.Check(m) {
		if i.Field == "license" && i.Severity == meta.Warning && strings.Contains(i.Message, "NC") {
			found = true
		}
	}
	if !found {
		t.Error("NC license must carry a reuse warning")
	}
}

func TestCheck_RelatedIdentifiers(t *testing.T) {
	m := validMeta()
	m.Related[0].Relation = "IsFriendsWith"
	if !hasError(meta.Check(m), "related[0].relation") {
		t.Error("unknown DataCite relationType must be an error")
	}
	m.Related[0].Relation = "IsSupplementTo"
	m.Related[0].Identifier = ""
	if !hasError(meta.Check(m), "related[0].identifier") {
		t.Error("empty related identifier must be an error")
	}
}

func TestCheck_Description(t *testing.T) {
	m := validMeta()
	m.Description = ""
	if !hasWarning(meta.Check(m), "description") {
		t.Error("missing description should warn")
	}
}

func TestCheckFiles(t *testing.T) {
	files := []manifest.DatasetFile{
		{Local: "a.csv", Key: "a.csv"},
		{Local: "empty.bin", Key: "empty.bin"},
	}
	sizes := map[string]int64{"a.csv": 10, "empty.bin": 0}
	issues := meta.CheckFiles(files, sizes, 100, 50)
	if !hasWarning(issues, "files[empty.bin]") {
		t.Errorf("zero-byte file should warn, got %+v", issues)
	}

	// Over the record's file-count cap: error.
	many := make([]manifest.DatasetFile, 3)
	for i := range many {
		many[i] = manifest.DatasetFile{Local: string(rune('a' + i)), Key: string(rune('a' + i))}
	}
	if !hasError(meta.CheckFiles(many, nil, 2, 0), "files") {
		t.Error("file count over the cap must be an error")
	}

	// Oversize file vs caps: error.
	big := []manifest.DatasetFile{{Local: "big", Key: "big"}}
	if !hasError(meta.CheckFiles(big, map[string]int64{"big": 100}, 100, 50), "files[big]") {
		t.Error("file over the size cap must be an error")
	}
}

// A dataset's resource_type must exist in the target instance's own
// vocabulary, probed at `remote add` time (issue #20). An empty vocabulary
// means nothing was probed — say nothing rather than guess.
func TestCheckResourceType(t *testing.T) {
	vocab := []string{"dataset", "software", "publication-article"}
	if issues := meta.CheckResourceType("dataset", vocab); len(issues) != 0 {
		t.Errorf("a vocabulary id must pass, got %+v", issues)
	}
	if issues := meta.CheckResourceType("", vocab); len(issues) != 0 {
		t.Errorf("an empty resource_type is defaulted elsewhere, got %+v", issues)
	}
	issues := meta.CheckResourceType("datset", vocab)
	if !hasError(issues, "resource_type") {
		t.Fatalf("an id outside the instance vocabulary must be an error, got %+v", issues)
	}
	if !strings.Contains(issues[0].Message, "dataset") {
		t.Errorf("the message should list what the instance offers, got %q", issues[0].Message)
	}
	if len(meta.CheckResourceType("anything", nil)) != 0 {
		t.Error("an unprobed (empty) vocabulary must not produce issues")
	}
}

func TestValidORCIDChecksums(t *testing.T) {
	// Well-known valid ORCIDs (checksum digit exercises the X case too).
	valid := []string{"0000-0002-1825-0097", "0000-0001-5109-3700", "0000-0002-1694-233X"}
	for _, o := range valid {
		m := validMeta()
		m.Creators[0].ORCID = o
		if hasError(meta.Check(m), "creators[0].orcid") {
			t.Errorf("valid ORCID %s rejected", o)
		}
	}
}
