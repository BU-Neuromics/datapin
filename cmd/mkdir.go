package cmd

import (
	"fmt"
	"os"
	"path"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var mkdirDryRun bool

var mkdirCmd = &cobra.Command{
	Use:   "mkdir <project>:<path>",
	Short: "Create a folder in an OSF project",
	Long: `Create a new folder in an OSF project's storage.

Intermediate parent directories must already exist. Use --dry-run to
preview without making any changes.

Examples:
  gosf mkdir abc12:/results/2026
  gosf mkdir abc12:/data/raw/batch-01
  gosf mkdir abc12:/scratch --dry-run`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := resolver.ParseTarget(args[0])
		if err != nil {
			return err
		}
		if target.Path == "/" || target.Path == "" {
			return fmt.Errorf("path required — specify the folder to create (e.g. abc12:/results)")
		}

		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("mkdir requires authentication — run 'gosf auth login' or set OSF_TOKEN")
		}

		folderPath := target.Path
		parentPath := path.Dir(folderPath)
		folderName := path.Base(folderPath)

		jsonMode := flagOutput == "json"
		result := output.MkdirResult{Path: folderPath}

		if mkdirDryRun {
			result.DryRun = true
			if jsonMode {
				return output.PrintJSON(os.Stdout, result)
			}
			log.Infof("[dry-run] would create folder %s in project %s", folderPath, target.NodeID)
			return nil
		}

		// osfstorage addresses folders by opaque ID, so the create URL must come
		// from the parent folder's ID-based link (or the storage root), never a
		// name-built path.
		res := resolver.New(client.New(token))
		base, err := folderUploadBase(cmd.Context(), res, target.NodeID, parentPath)
		if err != nil {
			return err
		}

		wb := client.NewWaterbutler(token)
		if err := wb.CreateFolder(cmd.Context(), client.AppendFolderName(base, folderName)); err != nil {
			return fmt.Errorf("creating folder %s: %w", folderPath, err)
		}

		result.Created = true
		if jsonMode {
			return output.PrintJSON(os.Stdout, result)
		}
		log.Infof("created %s", folderPath)
		return nil
	},
}

func init() {
	mkdirCmd.Flags().BoolVar(&mkdirDryRun, "dry-run", false, "Show what would be created without creating")
	rootCmd.AddCommand(mkdirCmd)
}
