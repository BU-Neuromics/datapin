package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var lsCmd = &cobra.Command{
	Use:   "ls <project>[:<path>]",
	Short: "List files in an OSF project or folder",
	Long: `List files and folders at the given OSF path.

Examples:
  datapin ls abc12                  # list root of project abc12
  datapin ls abc12:/data            # list the /data folder
  datapin ls abc12/xyz34:/results   # list /results in component xyz34`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := resolver.ParseTarget(args[0])
		if err != nil {
			return err
		}

		token := config.LoadToken(flagToken)
		c := client.New(token)
		res := resolver.New(c)

		log.Infof("listing %s:%s", target.NodeID, target.Path)
		items, err := res.ListDir(cmd.Context(), target.NodeID, target.Path)
		if err != nil {
			return friendlyAuthError(err)
		}

		if flagOutput == "json" {
			if items == nil {
				items = []client.FileItem{} // serialise as [] not null
			}
			return output.PrintJSON(os.Stdout, items)
		}

		if len(items) == 0 {
			log.Infof("(no files)")
			return nil
		}

		printFileTable(items)
		return nil
	},
}

func printFileTable(items []client.FileItem) {
	var rows [][]output.Cell
	for _, item := range items {
		name := item.Attributes.Name
		size := "—"
		var nameStyle func(string) string
		if item.Attributes.Kind == "folder" {
			name = name + "/"
			nameStyle = output.Cyan // folders stand out
		} else {
			size = output.FormatSize(item.Attributes.Size)
		}
		modified := output.FormatDate(item.Attributes.DateModified)
		rows = append(rows, []output.Cell{
			{Text: name, Style: nameStyle},
			{Text: size, Style: output.Dim},
			{Text: modified, Style: output.Dim},
		})
	}
	output.RenderTable(os.Stdout, []string{"NAME", "SIZE", "MODIFIED"}, rows)
}

func init() {
	rootCmd.AddCommand(lsCmd)
}
