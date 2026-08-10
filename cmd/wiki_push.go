package cmd

import (
	"bytes"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var wikiPushDryRun bool

var wikiPushCmd = &cobra.Command{
	Use:   "push <src.md> <project>[:<page>]",
	Short: "Create or update a wiki page from a local markdown file",
	Long: `Upload a local markdown file as a wiki page. If the page does not exist it
is created; if it exists, a new version is minted. A push whose content is
identical to the remote latest is skipped (no redundant version).

When :<page> is omitted the page name is derived from the file name
("docs/Analysis Notes.md" → page "Analysis Notes").

Examples:
  gosf wiki push docs/home.md abc12:home
  gosf wiki push "docs/Analysis Notes.md" abc12     # page "Analysis Notes"
  gosf wiki push docs/home.md abc12:home --dry-run`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		src := args[0]
		nodeID, page, err := parseWikiTarget(args[1], pageNameFromFile(src))
		if err != nil {
			return err
		}
		if err := validateWikiName(page); err != nil {
			return err
		}

		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("wiki push requires authentication — run 'gosf auth login' or set OSF_TOKEN")
		}

		content, err := os.ReadFile(src)
		if err != nil {
			return err
		}

		c := client.New(token)
		wikis, err := c.ListWikis(cmd.Context(), nodeID)
		if err != nil {
			return friendlyWikiError(err, nodeID)
		}

		result := output.WikiPushResult{Project: nodeID, DryRun: wikiPushDryRun}
		existing, exists := findWikiPage(wikis, page)

		switch {
		case !exists:
			result.Page = page
			result.Action = "create"
			result.Version = 1
			if wikiPushDryRun {
				log.Infof("[dry-run] ↑ would create wiki page %q on %s (%s)", page, nodeID, output.FormatSize(int64(len(content))))
				break
			}
			wiki, err := c.CreateWiki(cmd.Context(), nodeID, page, string(content))
			if err != nil {
				return friendlyWikiError(err, nodeID)
			}
			if wiki.Attributes.Extra.Version > 0 {
				result.Version = wiki.Attributes.Extra.Version
			}
			log.Infof("↑ created wiki page %q on %s (v%d)", page, nodeID, result.Version)

		default:
			result.Page = existing.Attributes.Name
			remote, err := c.GetWikiContent(cmd.Context(), existing.ID)
			if err != nil {
				return friendlyWikiError(err, nodeID)
			}
			if bytes.Equal(client.CanonicalizeWikiContent(remote), client.CanonicalizeWikiContent(content)) {
				result.Action = "skip"
				result.Version = existing.Attributes.Extra.Version
				log.Infof("≡ wiki page %q identical to remote v%d, skipping", result.Page, result.Version)
				break
			}
			result.Action = "update"
			result.Version = existing.Attributes.Extra.Version + 1
			if wikiPushDryRun {
				log.Infof("[dry-run] ↑ would push wiki page %q v%d → v%d", result.Page, existing.Attributes.Extra.Version, result.Version)
				break
			}
			v, err := c.CreateWikiVersion(cmd.Context(), existing.ID, string(content))
			if err != nil {
				return friendlyWikiError(err, nodeID)
			}
			if v.Number() > 0 {
				result.Version = v.Number()
			}
			log.Infof("↑ pushed wiki page %q v%d → v%d", result.Page, existing.Attributes.Extra.Version, result.Version)
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, result)
		}
		return nil
	},
}

// validateWikiName enforces OSF's wiki page name rules client-side so a bad
// name fails fast with a clear message instead of a server 400.
func validateWikiName(name string) error {
	if name == "" {
		return fmt.Errorf("wiki page name cannot be blank")
	}
	if len(name) > 100 {
		return fmt.Errorf("wiki page name cannot be longer than 100 characters")
	}
	// parseWikiTarget already rejects slashes in targets; re-check here for
	// names that arrive from other sources (mv's new name, filenames).
	for _, r := range name {
		if r == '/' {
			return fmt.Errorf("wiki page name %q cannot contain forward slashes", name)
		}
	}
	return nil
}

func init() {
	wikiPushCmd.Flags().BoolVar(&wikiPushDryRun, "dry-run", false, "Show what would happen without writing to OSF")
	wikiCmd.AddCommand(wikiPushCmd)
}
