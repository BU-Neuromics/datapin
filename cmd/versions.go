package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var versionsCmd = &cobra.Command{
	Use:   "versions <project>:<path>",
	Short: "List versions of a file in an OSF project",
	Long: `List all versions of a file in OSF Storage, newest first.

Requires a specific file path (folders are not supported).

Examples:
  gosf versions abc12:/data/results.csv
  gosf versions abc12:/data/results.csv --output=json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
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
