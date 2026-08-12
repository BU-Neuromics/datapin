package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/docgen"
	"github.com/BU-Neuromics/datapin/internal/log"
)

var (
	manOut  string
	manDate string
)

var manCmd = &cobra.Command{
	Use:   "man",
	Short: "Generate the datapin man page",
	Long: `Render the whole command tree as a roff man page on stdout, or into a
file with --out. HPC users reach for 'man', and a single binary has nowhere
to ship one from, so datapin generates its own.

  datapin man > /usr/local/share/man/man1/datapin.1
  datapin man --out man/datapin.1

The page is generated from the live command tree, so it can never drift from
the CLI. Pass --date for a reproducible build.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		date := manDate
		if date == "" {
			date = time.Now().UTC().Format("2006-01-02")
		}
		page := docgen.Man(rootCmd, version, date)

		if manOut == "" {
			_, err := os.Stdout.Write(page)
			return err
		}
		if dir := filepath.Dir(manOut); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}
		}
		if err := os.WriteFile(manOut, page, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", manOut, err)
		}
		log.Infof("man page written to %s", manOut)
		return nil
	},
}

func init() {
	manCmd.Flags().StringVar(&manOut, "out", "", "Write to this file instead of stdout")
	manCmd.Flags().StringVar(&manDate, "date", "", "Date stamped in the page header (default: today, UTC)")
	rootCmd.AddCommand(manCmd)
}
