package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var versionsCmd = &cobra.Command{
	Use:   "versions <project>:<path>",
	Short: "List a dataset's, a workspace file's, or an OSF file's versions",
	Long: `List version history, newest first.

A bare argument naming a manifest dataset lists its archive version chain
(version number, record id, DOI, and which one the manifest pins):
  datapin versions counts

Adding a file key lists that file's workspace journal instead — every push and
revert, and whether each version is still recoverable:
  datapin versions counts/results/counts.h5

An OSF path lists a stored file's versions (frozen legacy surface; requires a
specific file path — folders are not supported):
  datapin versions abc12:/data/results.csv
  datapin versions abc12:/data/results.csv --output=json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// A bare argument that names a manifest dataset lists the archive
		// record's version chain instead of an OSF file's versions.
		if !strings.Contains(args[0], ":") {
			if handled, err := runDatasetVersions(cmd.Context(), args[0]); handled {
				return err
			}
		}
		target, err := resolver.ParseTarget(args[0])
		if err != nil {
			return err
		}
		if target.Path == "" || target.Path == "/" {
			return fmt.Errorf("versions requires a specific file path, not a project root")
		}

		token := config.LoadToken(flagToken)
		c := client.New(token)
		res := resolver.New(c)

		log.Infof("fetching versions for %s", target.Path)
		item, err := res.Resolve(cmd.Context(), target.NodeID, target.Path)
		if err != nil {
			return friendlyAuthError(err)
		}
		if item.Attributes.Kind == "folder" {
			return fmt.Errorf("%q is a folder; versions only applies to files", target.Path)
		}

		versions, err := c.GetFileVersions(cmd.Context(), item.ID)
		if err != nil {
			return fmt.Errorf("fetching versions: %w", err)
		}

		if flagOutput == "json" {
			r := output.NewVersionsResult()
			for _, v := range versions {
				r.Versions = append(r.Versions, output.VersionItem{
					Version:     v.Number(),
					DateCreated: v.Attributes.DateCreated,
					Size:        v.Attributes.Size,
					Contributor: v.Contributor(),
				})
			}
			return output.PrintJSON(os.Stdout, r)
		}

		if len(versions) == 0 {
			log.Infof("no versions found")
			return nil
		}

		printVersionsTable(versions)
		return nil
	},
}

func printVersionsTable(versions []client.FileVersion) {
	var rows [][]output.Cell
	for i, v := range versions {
		// Highlight the latest (first, newest-first) version in cyan.
		var verStyle func(string) string
		if i == 0 {
			verStyle = output.Cyan
		}
		rows = append(rows, []output.Cell{
			{Text: fmt.Sprintf("%d", v.Number()), Style: verStyle},
			{Text: output.FormatDate(v.Attributes.DateCreated), Style: output.Dim},
			{Text: output.FormatSize(v.Attributes.Size)},
			{Text: v.Contributor()},
		})
	}
	output.RenderTable(os.Stdout, []string{"VERSION", "DATE", "SIZE", "CONTRIBUTOR"}, rows)
}

func init() {
	rootCmd.AddCommand(versionsCmd)
}
