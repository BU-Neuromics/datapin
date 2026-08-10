package cmd

import (
	"bufio"
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

var (
	rmDryRun bool
	rmYes    bool
)

var rmCmd = &cobra.Command{
	Use:   "rm <project>:<path>",
	Short: "Delete a file or folder from an OSF project",
	Long: `Delete a file or folder from an OSF project's storage.

Deleting a folder removes it and all its contents. You will be prompted
for confirmation unless --yes is supplied.

Examples:
  gosf rm abc12:/data/old-results.csv
  gosf rm abc12:/scratch/ --yes
  gosf rm abc12:/data/file.csv --dry-run`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := resolver.ParseTarget(args[0])
		if err != nil {
			return err
		}
		if target.Path == "/" || target.Path == "" {
			return fmt.Errorf("cowardly refusing to delete the root of a project")
		}

		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("rm requires authentication — run 'gosf auth login' or set OSF_TOKEN")
		}

		// Fail fast: JSON mode has no interactive prompt, so --yes is mandatory
		// for a real deletion. Check before doing any network work.
		if flagOutput == "json" && !rmDryRun && !rmYes {
			return fmt.Errorf("refusing to delete without --yes in --output=json mode")
		}

		osfClient := client.New(token)
		res := resolver.New(osfClient)

		// Resolve the path to the exact item (file or folder) and its links.
		item, err := res.Resolve(cmd.Context(), target.NodeID, target.Path)
		if err != nil {
			return err
		}

		if item.Links.Delete == "" {
			return fmt.Errorf("item has no delete URL (may not support deletion via API)")
		}

		jsonMode := flagOutput == "json"

		label := target.Path
		if item.Attributes.Kind == "folder" {
			label += " (and all contents)"
		}

		result := output.RemoveResult{
			Node: target.NodeID,
			Path: target.Path,
			Kind: item.Attributes.Kind,
		}

		if rmDryRun {
			result.DryRun = true
			if jsonMode {
				return output.PrintJSON(os.Stdout, result)
			}
			log.Infof("[dry-run] would delete %s from project %s", label, target.NodeID)
			return nil
		}

		// JSON mode already required --yes above; here handle interactive text mode.
		if !rmYes {
			if !confirm(fmt.Sprintf("Delete %s from project %s?", label, target.NodeID)) {
				log.Warnf("aborted")
				return nil
			}
		}

		wb := client.NewWaterbutler(token)
		if err := wb.Delete(cmd.Context(), item.Links.Delete); err != nil {
			return fmt.Errorf("deleting %s: %w", target.Path, err)
		}

		if jsonMode {
			return output.PrintJSON(os.Stdout, result)
		}
		log.Infof("deleted %s", target.Path)
		return nil
	},
}

// confirm prints a [y/N] prompt and returns true if the user types y/yes.
func confirm(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return answer == "y" || answer == "yes"
	}
	return false
}

func init() {
	rmCmd.Flags().BoolVar(&rmDryRun, "dry-run", false, "Show what would be deleted without deleting")
	rmCmd.Flags().BoolVarP(&rmYes, "yes", "y", false, "Skip confirmation prompt")
	rootCmd.AddCommand(rmCmd)
}
