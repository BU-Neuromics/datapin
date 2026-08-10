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

var (
	wikiRmDryRun bool
	wikiRmYes    bool
)

var wikiRmCmd = &cobra.Command{
	Use:   "rm <project>:<page>",
	Short: "Delete a wiki page from an OSF project",
	Long: `Delete a wiki page and its entire version history. You will be prompted
for confirmation unless --yes is supplied. The home page cannot be deleted.

Examples:
  gosf wiki rm abc12:scratch
  gosf wiki rm abc12:scratch --yes
  gosf wiki rm abc12:scratch --dry-run`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID, page, err := parseWikiTarget(args[0], "")
		if err != nil {
			return err
		}
		if isHomeWiki(page) {
			return fmt.Errorf("the home wiki page cannot be deleted (OSF requires every wiki to keep its home page)")
		}

		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("wiki rm requires authentication — run 'gosf auth login' or set OSF_TOKEN")
		}

		// JSON mode has no interactive prompt, so --yes is mandatory for a real
		// deletion. Check before doing any network work.
		if flagOutput == "json" && !wikiRmDryRun && !wikiRmYes {
			return fmt.Errorf("refusing to delete without --yes in --output=json mode")
		}

		c := client.New(token)
		wiki, err := resolveWikiPage(cmd.Context(), c, nodeID, page)
		if err != nil {
			return err
		}
		if isHomeWiki(wiki.Attributes.Name) {
			return fmt.Errorf("the home wiki page cannot be deleted (OSF requires every wiki to keep its home page)")
		}

		jsonMode := flagOutput == "json"
		result := output.WikiRemoveResult{Node: nodeID, Page: wiki.Attributes.Name}

		if wikiRmDryRun {
			result.DryRun = true
			if jsonMode {
				return output.PrintJSON(os.Stdout, result)
			}
			log.Infof("[dry-run] would delete wiki page %q (v%d and all history) from project %s",
				wiki.Attributes.Name, wiki.Attributes.Extra.Version, nodeID)
			return nil
		}

		if !wikiRmYes {
			if !confirm(fmt.Sprintf("Delete wiki page %q (v%d and all history) from project %s?",
				wiki.Attributes.Name, wiki.Attributes.Extra.Version, nodeID)) {
				log.Warnf("aborted")
				return nil
			}
		}

		if err := c.DeleteWiki(cmd.Context(), wiki.ID); err != nil {
			return fmt.Errorf("deleting wiki page %q: %w", wiki.Attributes.Name, friendlyWikiError(err, nodeID))
		}

		if jsonMode {
			return output.PrintJSON(os.Stdout, result)
		}
		log.Infof("deleted wiki page %q from project %s", wiki.Attributes.Name, nodeID)
		return nil
	},
}

func init() {
	wikiRmCmd.Flags().BoolVar(&wikiRmDryRun, "dry-run", false, "Show what would be deleted without deleting")
	wikiRmCmd.Flags().BoolVarP(&wikiRmYes, "yes", "y", false, "Skip confirmation prompt")
	wikiCmd.AddCommand(wikiRmCmd)
}
