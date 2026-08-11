// Package meta validates dataset metadata against the DataCite floor and
// FAIR conventions (plan §4.4): SPDX licenses, ORCID checksums, DataCite
// relation types. One internal model, standard serializers (see export.go)
// — never an invented schema.
package meta

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

//go:embed spdx_ids.txt
var spdxRaw string

// spdxIDs maps lowercased SPDX license ids to their canonical form.
var spdxIDs = func() map[string]string {
	out := map[string]string{}
	for _, id := range strings.Split(strings.TrimSpace(spdxRaw), "\n") {
		out[strings.ToLower(id)] = id
	}
	return out
}()

// relationTypes is the DataCite 4.x relationType vocabulary.
var relationTypes = map[string]bool{}

func init() {
	for _, r := range []string{
		"IsCitedBy", "Cites", "IsSupplementTo", "IsSupplementedBy",
		"IsContinuedBy", "Continues", "IsNewVersionOf", "IsPreviousVersionOf",
		"IsPartOf", "HasPart", "IsPublishedIn", "IsReferencedBy", "References",
		"IsDocumentedBy", "Documents", "IsCompiledBy", "Compiles",
		"IsVariantFormOf", "IsOriginalFormOf", "IsIdenticalTo", "HasMetadata",
		"IsMetadataFor", "Reviews", "IsReviewedBy", "IsDerivedFrom",
		"IsSourceOf", "Describes", "IsDescribedBy", "HasVersion",
		"IsVersionOf", "Obsoletes", "IsObsoletedBy", "Collects", "IsCollectedBy",
		"IsTranslationOf", "HasTranslation",
	} {
		relationTypes[r] = true
	}
}

// Severity grades an Issue.
type Severity string

const (
	Error   Severity = "error"   // blocks publish
	Warning Severity = "warning" // FAIR nudge, publish proceeds
)

// Issue is one finding from Check/CheckFiles.
type Issue struct {
	Severity Severity `json:"severity"`
	Field    string   `json:"field"`
	Message  string   `json:"message"`
}

// HasErrors reports whether any issue is an Error.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == Error {
			return true
		}
	}
	return false
}

// Check validates a dataset's metadata block. Errors block publish;
// warnings are FAIR nudges.
func Check(m manifest.DatasetMetadata) []Issue {
	var issues []Issue
	errf := func(field, format string, args ...any) {
		issues = append(issues, Issue{Error, field, fmt.Sprintf(format, args...)})
	}
	warnf := func(field, format string, args ...any) {
		issues = append(issues, Issue{Warning, field, fmt.Sprintf(format, args...)})
	}

	if m.Title == "" {
		errf("title", "a published record needs a title (DataCite mandatory)")
	}
	if m.Description == "" {
		warnf("description", "no description — reusers (and search engines) rely on it")
	}
	if len(m.Keywords) == 0 {
		warnf("keywords", "no keywords — they drive findability")
	}

	if len(m.Creators) == 0 {
		errf("creators", "a published record needs at least one creator (DataCite mandatory); add [[datasets.metadata.creators]] with name = \"Family, Given\"")
	}
	for i, c := range m.Creators {
		field := fmt.Sprintf("creators[%d]", i)
		if strings.TrimSpace(c.Name) == "" {
			errf(field+".name", "creator name cannot be blank")
		}
		switch orcid := NormalizeORCID(c.ORCID); {
		case c.ORCID == "":
			warnf(field+".orcid", "no ORCID for %q — ORCIDs make authorship unambiguous", c.Name)
		case orcid == "":
			errf(field+".orcid", "%q is not a valid ORCID (want 0000-0000-0000-000X with a valid checksum)", c.ORCID)
		}
	}

	if m.License == "" {
		warnf("license", "no license — without one, reuse is legally ambiguous (suggestion: CC0-1.0 for data)")
	} else {
		if _, ok := spdxIDs[strings.ToLower(m.License)]; !ok {
			msg := fmt.Sprintf("%q is not an SPDX license id", m.License)
			if hint := spdxSuggestion(m.License); hint != "" {
				msg += fmt.Sprintf(" — did you mean %q?", hint)
			}
			errf("license", "%s", msg)
		}
		up := strings.ToUpper(m.License)
		if strings.Contains(up, "-NC") || strings.Contains(up, "-ND") {
			warnf("license", "%s restricts reuse (NC/ND) — many journals and aggregators treat such data as closed", m.License)
		}
	}

	for i, r := range m.Related {
		field := fmt.Sprintf("related[%d]", i)
		if strings.TrimSpace(r.Identifier) == "" {
			errf(field+".identifier", "related identifier cannot be blank")
		}
		if !relationTypes[r.Relation] {
			errf(field+".relation", "%q is not a DataCite relationType (e.g. IsSupplementTo, IsDerivedFrom)", r.Relation)
		}
	}
	return issues
}

// CheckFiles validates the file set against backend caps. sizes maps
// local path → bytes (absent entries are skipped — missing files are the
// command's concern). maxFiles/maxFileSize of 0 mean unlimited.
func CheckFiles(files []manifest.DatasetFile, sizes map[string]int64, maxFiles int, maxFileSize int64) []Issue {
	var issues []Issue
	if maxFiles > 0 && len(files) > maxFiles {
		issues = append(issues, Issue{Error, "files",
			fmt.Sprintf("%d files exceed the record cap of %d — split the dataset or bundle files", len(files), maxFiles)})
	}
	for _, f := range files {
		sz, ok := sizes[f.Local]
		if !ok {
			continue
		}
		if sz == 0 {
			issues = append(issues, Issue{Warning, "files[" + f.Key + "]",
				"zero-byte file — publishing an empty file is almost never intended"})
		}
		if maxFileSize > 0 && sz > maxFileSize {
			issues = append(issues, Issue{Error, "files[" + f.Key + "]",
				fmt.Sprintf("file is larger than the backend's %d-byte cap", maxFileSize)})
		}
	}
	return issues
}

// NormalizeORCID returns the bare 0000-0000-0000-000X form, or "" when
// the input is not a valid ORCID. Accepts the https://orcid.org/ URL form.
func NormalizeORCID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://orcid.org/")
	s = strings.TrimPrefix(s, "http://orcid.org/")
	if len(s) != 19 {
		return ""
	}
	digits := make([]byte, 0, 16)
	for i, r := range s {
		if i == 4 || i == 9 || i == 14 {
			if r != '-' {
				return ""
			}
			continue
		}
		switch {
		case r >= '0' && r <= '9':
			digits = append(digits, byte(r))
		case (r == 'X' || r == 'x') && i == 18:
			digits = append(digits, 'X')
		default:
			return ""
		}
	}
	if !orcidChecksumOK(digits) {
		return ""
	}
	return strings.ToUpper(s)
}

// orcidChecksumOK verifies the ISO 7064 11-2 check digit over the 16
// base digits (last one is the check character).
func orcidChecksumOK(digits []byte) bool {
	total := 0
	for _, d := range digits[:15] {
		total = (total + int(d-'0')) * 2
	}
	remainder := total % 11
	result := (12 - remainder) % 11
	want := byte('0' + result)
	if result == 10 {
		want = 'X'
	}
	return digits[15] == want
}

// spdxSuggestion offers the obvious near-miss for a wrong license id
// (e.g. "CC0" → "CC0-1.0", "cc-by" → "CC-BY-4.0").
func spdxSuggestion(id string) string {
	low := strings.ToLower(id)
	if canonical, ok := spdxIDs[low]; ok {
		return canonical
	}
	// Common shorthand: the id with its latest version suffix.
	for _, suffix := range []string{"-1.0", "-4.0", "-3.0", "-2.0"} {
		if canonical, ok := spdxIDs[low+suffix]; ok {
			return canonical
		}
	}
	return ""
}
