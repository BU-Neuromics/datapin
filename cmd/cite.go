package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/meta"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var citeBibTeX bool

var citeCmd = &cobra.Command{
	Use:   "cite <slug>",
	Short: "Print a citation for a published dataset",
	Long: `Print a paste-ready citation for a published dataset, fetched via DOI
content negotiation (DataCite formats it). --bibtex emits BibTeX instead
of the default APA-style text.

Sandbox DOIs (prefix 10.5072) never resolve; for those a citation is
rendered locally from the manifest metadata. Set DATAPIN_DOI_BASE to
override the resolver (tests).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifestPath, _, err := manifest.FindManifest()
		if err != nil {
			return err
		}
		m, err := manifest.Load(manifestPath)
		if err != nil {
			return err
		}
		ds := m.FindDataset(args[0])
		if ds == nil {
			return fmt.Errorf("no dataset with slug %q in the manifest", args[0])
		}
		if ds.VersionDOI == "" {
			return fmt.Errorf("dataset %q has no DOI yet — publish it first (datapin publish %s)", ds.Slug, ds.Slug)
		}

		format := meta.FormatAPA
		if citeBibTeX {
			format = meta.FormatBibTeX
		}

		var citation string
		if strings.HasPrefix(ds.VersionDOI, "10.5072/") && os.Getenv("DATAPIN_DOI_BASE") == "" {
			log.Warnf("sandbox DOI %s does not resolve — rendering the citation locally", ds.VersionDOI)
			citation = meta.LocalCitation(ds, fmt.Sprintf("%d", time.Now().Year()))
		} else {
			citation, err = meta.Citation(cmd.Context(), os.Getenv("DATAPIN_DOI_BASE"), ds.VersionDOI, format)
			if err != nil {
				log.Warnf("%v — rendering the citation locally", err)
				citation = meta.LocalCitation(ds, fmt.Sprintf("%d", time.Now().Year()))
			}
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, map[string]any{
				"slug": ds.Slug, "doi": ds.VersionDOI, "concept_doi": ds.ConceptDOI,
				"format": string(format), "citation": citation,
			})
		}
		fmt.Fprintln(os.Stdout, citation)
		return nil
	},
}

func init() {
	citeCmd.Flags().BoolVar(&citeBibTeX, "bibtex", false, "Emit BibTeX instead of APA-style text")
	rootCmd.AddCommand(citeCmd)
}
