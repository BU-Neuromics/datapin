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

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List your OSF projects",
	Long: `List all OSF projects and components accessible to the authenticated user.

Requires a valid token (set via 'datapin auth login', --token flag, or OSF_TOKEN).`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("projects requires authentication — run 'datapin auth login' or set OSF_TOKEN")
		}

		c := client.New(token)
		log.Infof("fetching projects")
		nodes, err := c.GetUserNodes(cmd.Context())
		if err != nil {
			if apiErr, ok := err.(*client.APIError); ok && apiErr.StatusCode == 401 {
				return fmt.Errorf("invalid token — run 'datapin auth login' to re-authenticate")
			}
			return err
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, nodes)
		}

		printNodesTable(nodes)
		return nil
	},
}

func printNodesTable(nodes []client.Node) {
	if len(nodes) == 0 {
		log.Infof("no projects found")
		return
	}

	var rows [][]output.Cell
	for _, n := range nodes {
		vis := "private"
		visStyle := output.Dim
		if n.Attributes.Public {
			vis = "public"
			visStyle = output.Yellow // public is worth noticing
		}
		rows = append(rows, []output.Cell{
			{Text: n.Attributes.Title},
			{Text: n.ID, Style: output.Dim},
			{Text: vis, Style: visStyle},
			{Text: output.FormatDate(n.Attributes.DateModified), Style: output.Dim},
		})
	}
	output.RenderTable(os.Stdout, []string{"TITLE", "GUID", "VISIBILITY", "MODIFIED"}, rows)
}

func init() {
	rootCmd.AddCommand(projectsCmd)
}
