package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var wikiVersionsCmd = &cobra.Command{
	Use:   "versions <project>:<page>",
	Short: "List versions of a wiki page",
	Long: `List all versions of a wiki page, newest first.

Examples:
  datapin wiki versions abc12:home
  datapin wiki versions abc12:home --output=json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID, page, err := parseWikiTarget(args[0], "")
		if err != nil {
			return err
		}

		token := config.LoadToken(flagToken)
		c := client.New(token)

		wiki, err := resolveWikiPage(cmd.Context(), c, nodeID, page)
		if err != nil {
			return err
		}

		log.Infof("fetching versions for wiki %q", wiki.Attributes.Name)
		versions, err := c.GetWikiVersions(cmd.Context(), wiki.ID)
		if err != nil {
			return fmt.Errorf("fetching wiki versions: %w", friendlyWikiError(err, nodeID))
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

		var rows [][]output.Cell
		for i, v := range versions {
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
		return nil
	},
}

func init() {
	wikiCmd.AddCommand(wikiVersionsCmd)
}
