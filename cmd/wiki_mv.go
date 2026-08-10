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

var wikiMvDryRun bool

var wikiMvCmd = &cobra.Command{
	Use:   "mv <project>:<page> <new-name>",
	Short: "Rename a wiki page",
	Long: `Rename a wiki page. The home page cannot be renamed, and the new name must
not collide with an existing page.

Examples:
  gosf wiki mv abc12:draft "Final Protocol"
  gosf wiki mv abc12:draft final --dry-run`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID, page, err := parseWikiTarget(args[0], "")
		if err != nil {
			return err
		}
		newName := args[1]
		if isHomeWiki(page) {
			return fmt.Errorf("the home wiki page cannot be renamed")
		}
		if err := validateWikiName(newName); err != nil {
			return err
		}

		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("wiki mv requires authentication — run 'gosf auth login' or set OSF_TOKEN")
		}

		c := client.New(token)
		wiki, err := resolveWikiPage(cmd.Context(), c, nodeID, page)
		if err != nil {
			return err
		}
		if isHomeWiki(wiki.Attributes.Name) {
			return fmt.Errorf("the home wiki page cannot be renamed")
		}

		result := output.WikiMvResult{Node: nodeID, From: wiki.Attributes.Name, To: newName, DryRun: wikiMvDryRun}

		if wikiMvDryRun {
			if flagOutput == "json" {
				return output.PrintJSON(os.Stdout, result)
			}
			log.Infof("[dry-run] would rename wiki page %q → %q on project %s", wiki.Attributes.Name, newName, nodeID)
			return nil
		}

		if _, err := c.RenameWiki(cmd.Context(), wiki.ID, newName); err != nil {
			return fmt.Errorf("renaming wiki page %q: %w", wiki.Attributes.Name, friendlyWikiError(err, nodeID))
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, result)
		}
		log.Infof("renamed wiki page %q → %q on project %s", wiki.Attributes.Name, newName, nodeID)
		return nil
	},
}

func init() {
	wikiMvCmd.Flags().BoolVar(&wikiMvDryRun, "dry-run", false, "Show what would happen without renaming")
	wikiCmd.AddCommand(wikiMvCmd)
}
