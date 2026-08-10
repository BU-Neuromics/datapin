package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var initCmd = &cobra.Command{
	Use:   "init <project-id>",
	Short: "Initialize .gosf/gosf.toml with an OSF project ID",
	Long: `Create or update .gosf/gosf.toml in the current directory, setting [project].id.

If .gosf/gosf.toml already exists its [[files]] entries are preserved; only the
project ID is updated.

After running gosf init, bare 'gosf pull' and 'gosf push' will operate
against the configured project.

Examples:
  gosf init abc12
  gosf init abc12 --output=json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID := args[0]

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		path, created, err := manifest.Init(cwd, projectID)
		if err != nil {
			return err
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.InitResult{Project: projectID, Created: created})
		}

		if created {
			log.Infof("initialized gosf project %s (%s created)", projectID, path)
		} else {
			log.Infof("updated project ID to %s in %s", projectID, path)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
