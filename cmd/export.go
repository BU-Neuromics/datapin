package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/meta"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var (
	exportDatapackage bool
	exportROCrate     bool
	exportDir         string
)

var exportCmd = &cobra.Command{
	Use:   "export <slug>",
	Short: "Emit standard metadata files for a dataset",
	Long: `Write standard, machine-readable metadata files describing a dataset:

  --datapackage   datapackage.json (Data Package v2)
  --ro-crate      ro-crate-metadata.json (RO-Crate 1.2)

With neither flag, both are written. Files land in --dir (default: the
repository root). datapin emits existing standards; it never invents a
metadata schema.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifestPath, repoRoot, err := manifest.FindManifest()
		if err != nil {
			return err
		}
		m, err := manifest.Load(manifestPath)
		if err != nil {
			return err
		}
		ds := m.FindDataset(args[0])
		if ds == nil {
			return fmt.Errorf("no dataset with slug %q in the manifest", args[0])
		}

		_, sizes, _, err := datasetLocalState(repoRoot, ds)
		if err != nil {
			return err
		}

		dir := exportDir
		if dir == "" {
			dir = repoRoot
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}

		both := !exportDatapackage && !exportROCrate
		var written []string
		if exportDatapackage || both {
			data, err := meta.Datapackage(ds, sizes)
			if err != nil {
				return err
			}
			p := filepath.Join(dir, "datapackage.json")
			if err := os.WriteFile(p, append(data, '\n'), 0644); err != nil {
				return err
			}
			written = append(written, p)
		}
		if exportROCrate || both {
			data, err := meta.ROCrate(ds, sizes)
			if err != nil {
				return err
			}
			p := filepath.Join(dir, "ro-crate-metadata.json")
			if err := os.WriteFile(p, append(data, '\n'), 0644); err != nil {
				return err
			}
			written = append(written, p)
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, map[string]any{"slug": ds.Slug, "written": written})
		}
		for _, p := range written {
			log.Infof("wrote %s", p)
		}
		return nil
	},
}

func init() {
	exportCmd.Flags().BoolVar(&exportDatapackage, "datapackage", false, "Write datapackage.json (Data Package v2)")
	exportCmd.Flags().BoolVar(&exportROCrate, "ro-crate", false, "Write ro-crate-metadata.json (RO-Crate 1.2)")
	exportCmd.Flags().StringVar(&exportDir, "dir", "", "Directory to write into (default: repository root)")
	rootCmd.AddCommand(exportCmd)
}
