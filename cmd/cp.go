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
	cpDryRun   bool
	cpConflict string
)

var cpCmd = &cobra.Command{
	Use:   "cp <src> <dest>",
	Short: "Copy a file or folder in OSF Storage",
	Long: `Copy a file or folder within or across OSF projects.

Both src and dest must include a project GUID and path. The original
file is left in place; a copy is created at dest.

--conflict controls what happens if a file already exists at dest:
  keep    — append a suffix to avoid collision (default)
  replace — overwrite the existing file
  warn    — error and abort

Examples:
  datapin cp abc12:/raw/counts.h5 abc12:/backup/counts.h5
  datapin cp abc12:/templates/config.toml xyz34:/config.toml
  datapin cp abc12:/data/file.csv abc12:/results/output.csv --conflict replace`,
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
			return fmt.Errorf("cp requires authentication — run 'datapin auth login' or set OSF_TOKEN")
		}

		srcStr := args[0]
		destStr := args[1]
		jsonMode := flagOutput == "json"

		if cpDryRun {
			result := output.CpResult{Src: srcStr, Dest: destStr, DryRun: true}
			if jsonMode {
				return output.PrintJSON(os.Stdout, result)
			}
			log.Infof("[dry-run] would copy %s → %s", srcStr, destStr)
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

		destFolder := path.Dir(dest.Path)
		destName := path.Base(dest.Path)
		srcName := path.Base(src.Path)

		var rename string
		if destName != srcName {
			rename = destName
		}
		destNode := ""
		if src.NodeID != dest.NodeID {
			destNode = dest.NodeID
		}

		wb := client.NewWaterbutler(token)
		if err := wb.Copy(cmd.Context(), item.Links.Move, destNode, destFolder, rename, cpConflict); err != nil {
			return fmt.Errorf("copying %s: %w", srcStr, err)
		}

		result := output.CpResult{Src: srcStr, Dest: destStr}
		if jsonMode {
			return output.PrintJSON(os.Stdout, result)
		}
		log.Infof("copied %s → %s", srcStr, destStr)
		return nil
	},
}

func init() {
	cpCmd.Flags().BoolVar(&cpDryRun, "dry-run", false, "Show what would be copied without copying")
	cpCmd.Flags().StringVar(&cpConflict, "conflict", "keep", "Conflict resolution: keep, replace, warn")
	rootCmd.AddCommand(cpCmd)
}
