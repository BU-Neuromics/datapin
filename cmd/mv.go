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

var (
	mvDryRun   bool
	mvConflict string
)

var mvCmd = &cobra.Command{
	Use:   "mv <src> <dest>",
	Short: "Move or rename a file or folder in OSF Storage",
	Long: `Move or rename a file or folder within or across OSF projects.

Both src and dest must include a project GUID and path. When src and dest
share the same parent folder, the operation is a rename. Otherwise the
file is moved to the destination folder (and optionally renamed).

--conflict controls what happens if a file already exists at dest:
  warn    — error and abort (default)
  replace — overwrite the existing file
  keep    — upload as a new name (dest_1.ext, dest_2.ext, …)

Examples:
  gosf mv abc12:/raw/counts.h5 abc12:/raw/counts_v2.h5
  gosf mv abc12:/raw/counts.h5 abc12:/processed/counts.h5
  gosf mv abc12:/raw/counts.h5 xyz34:/archive/counts.h5
  gosf mv abc12:/data/file.csv abc12:/results/output.csv --conflict replace`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		src, err := resolver.ParseTarget(args[0])
		if err != nil {
			return fmt.Errorf("src: %w", err)
		}
		dest, err := resolver.ParseTarget(args[1])
		if err != nil {
			return fmt.Errorf("dest: %w", err)
		}
		if src.Path == "/" {
			return fmt.Errorf("src must be a specific file or folder, not the project root")
		}
		if dest.Path == "/" {
			return fmt.Errorf("dest must include a path, not just a project GUID")
		}

		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("mv requires authentication — run 'gosf auth login' or set OSF_TOKEN")
		}

		srcStr := args[0]
		destStr := args[1]
		jsonMode := flagOutput == "json"

		if mvDryRun {
			result := output.MvResult{Src: srcStr, Dest: destStr, DryRun: true}
			if jsonMode {
				return output.PrintJSON(os.Stdout, result)
			}
			log.Infof("[dry-run] would move %s → %s", srcStr, destStr)
			return nil
		}

		osfClient := client.New(token)
		res := resolver.New(osfClient)

		item, err := res.Resolve(cmd.Context(), src.NodeID, src.Path)
		if err != nil {
			return fmt.Errorf("resolving src %s: %w", srcStr, err)
		}
		if item.Links.Move == "" {
			return fmt.Errorf("item has no move URL")
		}

		srcFolder := path.Dir(src.Path)
		destFolder := path.Dir(dest.Path)
		destName := path.Base(dest.Path)
		srcName := path.Base(src.Path)

		wb := client.NewWaterbutler(token)

		sameProject := src.NodeID == dest.NodeID
		sameFolder := sameProject && srcFolder == destFolder

		if sameFolder {
			// Pure rename — same location, different name.
			if err := wb.Rename(cmd.Context(), item.Links.Move, destName); err != nil {
				return fmt.Errorf("renaming %s: %w", srcStr, err)
			}
		} else {
			// Move (cross-folder or cross-project), with optional rename.
			var rename string
			if destName != srcName {
				rename = destName
			}
			destNode := ""
			if !sameProject {
				destNode = dest.NodeID
			}
			if err := wb.Move(cmd.Context(), item.Links.Move, destNode, destFolder, rename, mvConflict); err != nil {
				return fmt.Errorf("moving %s: %w", srcStr, err)
			}
		}

		result := output.MvResult{Src: srcStr, Dest: destStr}
		if jsonMode {
			return output.PrintJSON(os.Stdout, result)
		}
		log.Infof("moved %s → %s", srcStr, destStr)
		return nil
	},
}

func init() {
	mvCmd.Flags().BoolVar(&mvDryRun, "dry-run", false, "Show what would be moved without moving")
	mvCmd.Flags().StringVar(&mvConflict, "conflict", "warn", "Conflict resolution: warn, replace, keep")
	rootCmd.AddCommand(mvCmd)
}
