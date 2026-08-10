package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var wikiOpenCmd = &cobra.Command{
	Use:   "open <project>[:<page>]",
	Short: "Open a wiki page in the browser",
	Long: `Constructs the osf.io URL for a wiki page and opens it in the default
browser. On headless systems, prints the URL instead. The page defaults to "home".

Examples:
  datapin wiki open abc12                    # opens https://osf.io/abc12/wiki/home/
  datapin wiki open "abc12:Analysis Notes"`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID, page, err := parseWikiTarget(args[0], "home")
		if err != nil {
			return err
		}

		osfURL := wikiWebURL(nodeID, page)

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.OpenResult{URL: osfURL})
		}

		if err := openBrowser(osfURL); err != nil {
			fmt.Fprintln(os.Stdout, osfURL)
			return nil
		}

		log.Infof("opened %s", osfURL)
		return nil
	},
}

func init() {
	wikiCmd.AddCommand(wikiOpenCmd)
}
