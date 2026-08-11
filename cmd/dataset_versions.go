package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
)

// datasetVersionItem is one row of `datapin versions <slug> --output=json`.
type datasetVersionItem struct {
	Version int    `json:"version"`
	Record  string `json:"record"`
	DOI     string `json:"doi"`
	Latest  bool   `json:"latest"`
	Pinned  bool   `json:"pinned"`
	Created string `json:"created,omitempty"`
}

// runDatasetVersions lists the archive version chain for a dataset slug.
// Returns handled=false when the argument names no dataset (the caller
// falls through to OSF path handling).
func runDatasetVersions(ctx context.Context, slug string) (handled bool, err error) {
	manifestPath, _, err := manifest.FindManifest()
	if err != nil {
		if manifest.IsNotFound(err) {
			return false, nil
		}
		return true, err
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return true, err
	}
	ds, key := splitSlugKey(m, slug)
	if ds == nil {
		return false, nil
	}
	if key != "" {
		// <slug>/<key>: the workspace journal for one file.
		return true, runWorkspaceVersions(ctx, m, ds, key)
	}

	if ds.Record == "" {
		return true, fmt.Errorf("dataset %q has never been published — no versions yet (datapin publish %s)", slug, slug)
	}
	bk, _, err := resolveArchive(ds, m)
	if err != nil {
		return true, err
	}
	rec, err := bk.GetRecord(ctx, backend.RecordID(ds.Record))
	if err != nil {
		return true, err
	}

	items := make([]datasetVersionItem, 0, len(rec.Versions))
	for i := len(rec.Versions) - 1; i >= 0; i-- { // newest first, like file versions
		v := rec.Versions[i]
		items = append(items, datasetVersionItem{
			Version: v.Index + 1,
			Record:  string(v.ID),
			DOI:     v.DOI,
			Latest:  v.IsLatest,
			Pinned:  v.Index+1 == ds.Version,
			Created: v.Created,
		})
	}

	if flagOutput == "json" {
		return true, output.PrintJSON(os.Stdout, map[string]any{
			"slug": slug, "concept_doi": rec.ConceptDOI, "versions": items,
		})
	}

	rows := make([][]output.Cell, 0, len(items))
	for _, it := range items {
		marks := ""
		style := output.Dim
		if it.Latest {
			marks = "latest"
			style = output.Green
		}
		if it.Pinned {
			if marks != "" {
				marks += ", "
			}
			marks += "pinned"
			style = output.Bold
		}
		rows = append(rows, []output.Cell{
			{Text: fmt.Sprintf("v%d", it.Version), Style: output.Bold},
			{Text: it.Record},
			{Text: it.DOI},
			{Text: marks, Style: style},
		})
	}
	output.RenderTable(os.Stdout, []string{"VERSION", "RECORD", "DOI", ""}, rows)
	if rec.ConceptDOI != "" {
		fmt.Fprintf(os.Stderr, "concept DOI (always latest): https://doi.org/%s\n", rec.ConceptDOI)
	}
	return true, nil
}
