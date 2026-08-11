package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var openCmd = &cobra.Command{
	Use:   "open <project>[:<path>]",
	Short: "Open an OSF project or file in the browser",
	Long: `Constructs the osf.io URL for the given project or path and opens it in
the default browser. On headless systems, prints the URL instead.

Examples:
  datapin open abc12                  # opens https://osf.io/abc12/
  datapin open abc12:/data            # opens the /data folder in the OSF web UI
  datapin open abc12:/data/file.csv   # opens the file in the OSF web UI`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// A bare argument naming a manifest dataset opens its published
		// record's landing page on the archive.
		if !strings.Contains(args[0], ":") {
			if handled, err := runDatasetOpen(args[0]); handled {
				return err
			}
		}
		target, err := resolver.ParseTarget(args[0])
		if err != nil {
			return err
		}

		osfURL := buildOSFWebURL(target)

		// In JSON mode, emit the URL for scripting and do not launch a browser.
		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.OpenResult{URL: osfURL})
		}

		if err := openBrowser(osfURL); err != nil {
			// On headless systems the open command may fail — just print the URL.
			fmt.Fprintln(os.Stdout, osfURL)
			return nil
		}

		log.Infof("opened %s", osfURL)
		return nil
	},
}

func buildOSFWebURL(t resolver.Target) string {
	if t.Path == "/" || t.Path == "" {
		return fmt.Sprintf("https://osf.io/%s/", t.NodeID)
	}
	// Strip leading slash; OSF web URLs use /files/osfstorage/<path>
	path := strings.TrimPrefix(t.Path, "/")
	return fmt.Sprintf("https://osf.io/%s/files/osfstorage/%s", t.NodeID, path)
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

func init() {
	rootCmd.AddCommand(openCmd)
}
