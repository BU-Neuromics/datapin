package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
)

// runDatasetOpen opens a dataset's record landing page on its archive
// remote (works for sandbox records too, whose DOIs never resolve).
// Returns handled=false when slug names no dataset.
func runDatasetOpen(slug string) (handled bool, err error) {
	manifestPath, _, err := manifest.FindManifest()
	if err != nil {
		if manifest.IsNotFound(err) {
			return false, nil
		}
		return true, err
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return true, err
	}
	ds := m.FindDataset(slug)
	if ds == nil {
		return false, nil
	}
	if ds.Record == "" {
		return true, fmt.Errorf("dataset %q has never been published — nothing to open (datapin publish %s)", slug, slug)
	}
	_, remote, err := resolveArchive(ds, m)
	if err != nil {
		return true, err
	}
	url := strings.TrimSuffix(remote.URL, "/") + "/records/" + ds.Record

	if flagOutput == "json" {
		return true, output.PrintJSON(os.Stdout, output.OpenResult{URL: url})
	}
	if err := openBrowser(url); err != nil {
		fmt.Fprintln(os.Stdout, url)
		return true, nil
	}
	log.Infof("opened %s", url)
	return true, nil
}
