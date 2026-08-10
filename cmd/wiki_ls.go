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

var wikiLsCmd = &cobra.Command{
	Use:   "ls <project>",
	Short: "List wiki pages of an OSF project",
	Long: `List the wiki pages of an OSF project, most recently modified first.

Examples:
  datapin wiki ls abc12
  datapin wiki ls abc12 --output=json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID, _, err := parseWikiTarget(args[0], "home")
		if err != nil {
			return err
		}

		token := config.LoadToken(flagToken)
		c := client.New(token)

		log.Infof("listing wiki pages of %s", nodeID)
		wikis, err := c.ListWikis(cmd.Context(), nodeID)
		if err != nil {
			return friendlyWikiError(err, nodeID)
		}

		if flagOutput == "json" {
			items := make([]output.WikiListItem, 0, len(wikis))
			for _, w := range wikis {
				items = append(items, output.WikiListItem{
					ID:           w.ID,
					Name:         w.Attributes.Name,
					Version:      w.Attributes.Extra.Version,
					Size:         w.Attributes.Size,
					DateModified: w.Attributes.DateModified,
				})
			}
			return output.PrintJSON(os.Stdout, items)
		}

		if len(wikis) == 0 {
			log.Infof("no wiki pages")
			return nil
		}

		var rows [][]output.Cell
		for _, w := range wikis {
			var nameStyle func(string) string
			if isHomeWiki(w.Attributes.Name) {
				nameStyle = output.Bold
			}
			rows = append(rows, []output.Cell{
				{Text: w.Attributes.Name, Style: nameStyle},
				{Text: fmt.Sprintf("v%d", w.Attributes.Extra.Version), Style: output.Cyan},
				{Text: output.FormatSize(w.Attributes.Size)},
				{Text: output.FormatDate(w.Attributes.DateModified), Style: output.Dim},
			})
		}
		output.RenderTable(os.Stdout, []string{"NAME", "VER", "SIZE", "MODIFIED"}, rows)
		return nil
	},
}

func init() {
	wikiCmd.AddCommand(wikiLsCmd)
}
