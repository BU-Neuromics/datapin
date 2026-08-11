package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/meta"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var checkFAIR bool

var checkCmd = &cobra.Command{
	Use:   "check [<slug>]",
	Short: "Lint dataset metadata against the DataCite floor and FAIR conventions",
	Long: `Validate every dataset's metadata (or one dataset's, by slug):
DataCite-mandatory fields (title, creators), SPDX license ids, ORCID
checksums, DataCite relation types, plus file-level preflights (zero-byte
files, backend caps). Errors are what 'datapin publish' will refuse;
warnings are FAIR nudges.

--fair additionally runs a full FAIR assessment of each published
dataset's DOI through an F-UJI server (F-UJI probes the public record, so
it only works for resolving, non-sandbox DOIs). The endpoint defaults to
the hosted https://www.f-uji.net service, which requires credentials:
set DATAPIN_FUJI_URL, DATAPIN_FUJI_USER, DATAPIN_FUJI_PASS to use it or
a self-hosted instance.

Exit code: 0 when no errors (warnings allowed), 1 otherwise.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifestPath, repoRoot, err := manifest.FindManifest()
		if err != nil {
			return err
		}
		m, err := manifest.Load(manifestPath)
		if err != nil {
			return err
		}

		var targets []*manifest.Dataset
		if len(args) == 1 {
			ds := m.FindDataset(args[0])
			if ds == nil {
				return fmt.Errorf("no dataset with slug %q in the manifest", args[0])
			}
			targets = append(targets, ds)
		} else {
			for i := range m.Datasets {
				targets = append(targets, &m.Datasets[i])
			}
		}
		if len(targets) == 0 {
			return fmt.Errorf("no [[datasets]] in the manifest — nothing to check")
		}

		type checkResult struct {
			Slug   string          `json:"slug"`
			Issues []meta.Issue    `json:"issues"`
			FAIR   *meta.FAIRScore `json:"fair,omitempty"`
		}
		var results []checkResult
		anyErrors := false

		for _, ds := range targets {
			issues := meta.Check(ds.Metadata)

			// File-level preflight against the archive's caps where the
			// remote is configured; sizes for files that exist locally.
			// The caps are the remote's stored (probed or hand-edited) ones
			// — newArchiveBackend overlays them (issue #20).
			maxFiles, maxSize := 0, int64(0)
			if bk, r, err := resolveArchive(ds, m); err == nil {
				caps := bk.Capabilities()
				maxFiles, maxSize = caps.MaxFilesPerRecord, caps.MaxFileSize
				// A resource_type outside the instance's own vocabulary is
				// what publish would reject; only a probed vocabulary can
				// say so (an unprobed remote produces no issue).
				if r.Caps != nil {
					issues = append(issues, meta.CheckResourceType(ds.Metadata.ResourceType, r.Caps.ResourceTypes)...)
				}
			}
			_, sizes, _, err := datasetLocalState(repoRoot, ds)
			if err != nil {
				return err
			}
			bySize := make(map[string]int64, len(sizes))
			for local, sz := range sizes {
				bySize[local] = sz
			}
			issues = append(issues, meta.CheckFiles(ds.Files, bySize, maxFiles, maxSize)...)

			res := checkResult{Slug: ds.Slug, Issues: issues}
			if meta.HasErrors(issues) {
				anyErrors = true
			}

			if checkFAIR {
				if ds.VersionDOI == "" {
					log.Warnf("dataset %q: --fair needs a published DOI — skipping", ds.Slug)
				} else {
					fuji := meta.NewFUJIClient(
						envOr("DATAPIN_FUJI_URL", "https://www.f-uji.net/fuji/api/v1/evaluate"),
						os.Getenv("DATAPIN_FUJI_USER"), os.Getenv("DATAPIN_FUJI_PASS"))
					log.Infof("dataset %q: running F-UJI assessment of %s (can take a minute)", ds.Slug, ds.VersionDOI)
					score, err := fuji.Evaluate(cmd.Context(), ds.VersionDOI)
					if err != nil {
						return fmt.Errorf("F-UJI assessment of %s: %w", ds.VersionDOI, err)
					}
					res.FAIR = &score
				}
			}
			results = append(results, res)
		}

		if flagOutput == "json" {
			if err := output.PrintJSON(os.Stdout, results); err != nil {
				return err
			}
		} else {
			var rows [][]output.Cell
			for _, r := range results {
				for _, i := range r.Issues {
					style := output.Yellow
					if i.Severity == meta.Error {
						style = output.Red
					}
					rows = append(rows, []output.Cell{
						{Text: string(i.Severity), Style: style},
						{Text: r.Slug},
						{Text: i.Field},
						{Text: i.Message},
					})
				}
				if r.FAIR != nil {
					rows = append(rows, []output.Cell{
						{Text: "fair", Style: output.Cyan},
						{Text: r.Slug},
						{Text: "F-UJI"},
						{Text: fmt.Sprintf("FAIR %.0f%% (F %.0f%% / A %.0f%% / I %.0f%% / R %.0f%%)",
							r.FAIR.FAIR, r.FAIR.F, r.FAIR.A, r.FAIR.I, r.FAIR.R)},
					})
				}
			}
			if len(rows) == 0 {
				log.Infof("all datasets clean — publish-ready")
			} else {
				output.RenderTable(os.Stdout, []string{"SEVERITY", "DATASET", "FIELD", "MESSAGE"}, rows)
			}
		}

		if anyErrors {
			return &exitCodeError{code: 1}
		}
		return nil
	},
}

// envOr returns the env var's value or a default.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func init() {
	checkCmd.Flags().BoolVar(&checkFAIR, "fair", false, "Run an F-UJI FAIR assessment of each published DOI")
	rootCmd.AddCommand(checkCmd)
}
