package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var wikiAddCmd = &cobra.Command{
	Use:   "add <local.md> [<project>:]<page>",
	Short: "Track a local markdown file as a wiki page in .datapin/datapin.toml",
	Long: `Add a local markdown file to the manifest as a wiki page, so it syncs with
'datapin status' and 'datapin sync' like a tracked file.

If <page> is omitted the page name is derived from the file name. If the page
already exists on OSF, its current version and content MD5 are pinned;
otherwise the entry starts unpinned (version 0).

Examples:
  datapin wiki add docs/home.md home
  datapin wiki add docs/home.md abc12:home
  datapin wiki add "docs/Analysis Notes.md"     # page "Analysis Notes"`,
	Args:         cobra.RangeArgs(1, 2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		src := args[0]

		// Parse the optional <project>:<page> destination.
		var nodeID, page string
		if len(args) == 2 {
			var err error
			nodeID, page, err = parseWikiTarget(args[1], pageNameFromFile(src))
			if err != nil {
				return err
			}
		} else {
			page = pageNameFromFile(src)
		}
		if err := manifestWikiName(page); err != nil {
			return err
		}

		// Load or create the manifest.
		manifestPath, _, findErr := manifest.FindManifest()
		var m *manifest.Manifest
		manifestCreated := false
		if manifest.IsNotFound(findErr) {
			if nodeID == "" {
				return fmt.Errorf("no project configured — run: datapin init <project-id>")
			}
			manifestPath = filepath.Join(".datapin", "datapin.toml")
			m = &manifest.Manifest{Project: manifest.ProjectConfig{ID: nodeID}}
			manifestCreated = true
		} else if findErr != nil {
			return findErr
		} else {
			var err error
			m, err = manifest.Load(manifestPath)
			if err != nil {
				return err
			}
		}

		if nodeID == "" {
			nodeID = m.Project.ID
		}
		if nodeID == "" {
			return fmt.Errorf("no project configured — run: datapin init <project-id>")
		}
		entryProject := ""
		if nodeID != m.Project.ID {
			entryProject = nodeID
		}

		if findWikiEntryByLocal(m, src) >= 0 || findEntryByLocal(m, src) >= 0 {
			return fmt.Errorf("entry with local path %q already exists in .datapin/datapin.toml", src)
		}

		entry := manifest.WikiEntry{
			Local:   src,
			Page:    page,
			Project: entryProject,
		}

		// Pin to the remote if the page already exists.
		token := config.LoadToken(flagToken)
		c := client.New(token)
		if wikis, err := c.ListWikis(cmd.Context(), nodeID); err == nil {
			if existing, ok := findWikiPage(wikis, page); ok {
				if content, err := c.GetWikiContent(cmd.Context(), existing.ID); err == nil {
					entry.Version = existing.Attributes.Extra.Version
					entry.MD5 = wikiContentMD5(content)
					entry.Page = existing.Attributes.Name
				}
			}
		}

		m.Wikis = append(m.Wikis, entry)
		if err := manifest.Save(m, manifestPath); err != nil {
			return err
		}

		addEntry := output.WikiAddEntry{
			Local:   entry.Local,
			Page:    entry.Page,
			Project: nodeID,
			Version: entry.Version,
			MD5:     entry.MD5,
		}
		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.WikiAddResult{
				Entries:         []output.WikiAddEntry{addEntry},
				ManifestCreated: manifestCreated,
			})
		}
		if entry.Version > 0 {
			log.Infof("added wiki %s → %s:%s  (v%d)", entry.Local, nodeID, entry.Page, entry.Version)
		} else {
			log.Infof("added wiki %s → %s:%s  (not yet pushed)", entry.Local, nodeID, entry.Page)
		}
		return nil
	},
}

// manifestWikiName validates a page name for a manifest entry (same rules as
// the manifest loader, applied up front for a clean error).
func manifestWikiName(page string) error {
	if page == "" {
		return fmt.Errorf("wiki page name cannot be blank")
	}
	if len(page) > 100 {
		return fmt.Errorf("wiki page name cannot be longer than 100 characters")
	}
	for _, r := range page {
		if r == '/' {
			return fmt.Errorf("wiki page name %q cannot contain forward slashes", page)
		}
	}
	return nil
}

// findWikiEntryByLocal returns the index of the wiki entry with the given local
// path, or -1.
func findWikiEntryByLocal(m *manifest.Manifest, local string) int {
	for i, w := range m.Wikis {
		if w.Local == local {
			return i
		}
	}
	return -1
}

func init() {
	wikiCmd.AddCommand(wikiAddCmd)
}
