package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var (
	wikiGetVersion int
	wikiGetForce   bool
)

var wikiGetCmd = &cobra.Command{
	Use:   "get <project>[:<page>] [dest]",
	Short: "Print (or download) the content of a wiki page",
	Long: `Fetch the markdown content of a wiki page. The page defaults to "home".

By default the content is printed to stdout, so it pipes cleanly:
  datapin wiki get abc12 | less

With a dest argument the content is written to a file instead:
  datapin wiki get abc12:protocol protocol.md

Examples:
  datapin wiki get abc12                      # home page to stdout
  datapin wiki get "abc12:Analysis Notes"     # named page (quote spaces)
  datapin wiki get abc12:home --version=2     # a historical version
  datapin wiki get abc12:home docs/home.md    # write to a file`,
	Args:         cobra.RangeArgs(1, 2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID, page, err := parseWikiTarget(args[0], "home")
		if err != nil {
			return err
		}

		token := config.LoadToken(flagToken)
		c := client.New(token)

		wiki, err := resolveWikiPage(cmd.Context(), c, nodeID, page)
		if err != nil {
			return err
		}

		version := wiki.Attributes.Extra.Version
		var content []byte
		if wikiGetVersion > 0 {
			if wikiGetVersion > version {
				return fmt.Errorf("version %d of wiki page %q does not exist (latest is v%d)", wikiGetVersion, wiki.Attributes.Name, version)
			}
			version = wikiGetVersion
			content, err = c.GetWikiVersionContent(cmd.Context(), wiki.ID, strconv.Itoa(wikiGetVersion))
		} else {
			content, err = c.GetWikiContent(cmd.Context(), wiki.ID)
		}
		if err != nil {
			return friendlyWikiError(err, nodeID)
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.WikiGetResult{
				Project: nodeID,
				Page:    wiki.Attributes.Name,
				Version: version,
				Size:    int64(len(content)),
				Content: string(content),
			})
		}

		if len(args) == 2 {
			dest := args[1]
			if _, statErr := os.Stat(dest); statErr == nil && !wikiGetForce {
				return fmt.Errorf("%s already exists — pass --force to overwrite", dest)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(dest, content, 0644); err != nil {
				return err
			}
			log.Infof("↓ wrote %s (wiki %q v%d, %s)", dest, wiki.Attributes.Name, version, output.FormatSize(int64(len(content))))
			return nil
		}

		_, err = os.Stdout.Write(content)
		return err
	},
}

func init() {
	wikiGetCmd.Flags().IntVar(&wikiGetVersion, "version", 0, "Fetch a specific historical version")
	wikiGetCmd.Flags().BoolVar(&wikiGetForce, "force", false, "Overwrite an existing dest file")
	wikiCmd.AddCommand(wikiGetCmd)
}
