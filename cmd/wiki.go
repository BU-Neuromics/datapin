package cmd

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var wikiCmd = &cobra.Command{
	Use:   "wiki",
	Short: "Read, write, and sync OSF project wiki pages",
	Long: `Work with OSF project wikis: versioned markdown pages attached to a project.

Wiki pages are addressed as <project>:<page-name>. Page names live in a flat
namespace (they are not paths) and may contain spaces — quote them in the shell.
Where a page name is optional it defaults to "home", the page OSF shows first.

Track pages in .gosf/gosf.toml with 'gosf wiki add' to sync them like files
(gosf status / gosf sync).`,
}

// parseWikiTarget parses a <project>[:<page>] argument. The node part accepts
// the same forms as file targets (abc12 or abc12/xyz34 for components). When
// the page is omitted or empty, defaultPage is used; an empty defaultPage
// means the page is required.
func parseWikiTarget(s, defaultPage string) (nodeID, page string, err error) {
	if s == "" {
		return "", "", fmt.Errorf("empty target")
	}
	nodePart := s
	if idx := strings.IndexByte(s, ':'); idx >= 0 {
		nodePart = s[:idx]
		page = s[idx+1:]
	}
	if page == "" {
		page = defaultPage
	}
	if page == "" {
		return "", "", fmt.Errorf("%q: wiki page name required (use <project>:<page>)", s)
	}
	if strings.Contains(page, "/") {
		return "", "", fmt.Errorf("wiki page name %q cannot contain forward slashes", page)
	}
	t, err := resolver.ParseTarget(nodePart)
	if err != nil {
		return "", "", err
	}
	return t.NodeID, page, nil
}

// pageNameFromFile derives a wiki page name from a local file path: the base
// name with a markdown extension (.md / .markdown, case-insensitive) removed.
func pageNameFromFile(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	switch strings.ToLower(ext) {
	case ".md", ".markdown":
		return strings.TrimSuffix(base, ext)
	}
	return base
}

// wikiWebURL returns the osf.io web URL for a wiki page.
func wikiWebURL(nodeID, page string) string {
	return fmt.Sprintf("https://osf.io/%s/wiki/%s/", nodeID, url.PathEscape(page))
}

// findWikiPage locates a page by name: an exact match wins; otherwise a
// case-insensitive match is accepted only when it is unambiguous.
func findWikiPage(wikis []client.Wiki, name string) (*client.Wiki, bool) {
	for i := range wikis {
		if wikis[i].Attributes.Name == name {
			return &wikis[i], true
		}
	}
	var ci *client.Wiki
	for i := range wikis {
		if strings.EqualFold(wikis[i].Attributes.Name, name) {
			if ci != nil {
				return nil, false // ambiguous
			}
			ci = &wikis[i]
		}
	}
	return ci, ci != nil
}

// isHomeWiki reports whether a page name refers to the special home page,
// which OSF refuses to rename or delete.
func isHomeWiki(name string) bool {
	return strings.EqualFold(name, "home")
}

// friendlyWikiError maps common wiki API failures to actionable messages:
// the addon-disabled 404 and auth errors; everything else passes through.
func friendlyWikiError(err error, nodeID string) error {
	if err == nil {
		return nil
	}
	if client.IsWikiDisabled(err) {
		return fmt.Errorf("the wiki for project %s is disabled — enable it on osf.io under the project's Settings → Select Add-ons → Wiki", nodeID)
	}
	return friendlyAuthError(err)
}

// resolveWikiPage lists a node's wiki pages and finds one by name.
func resolveWikiPage(ctx context.Context, c *client.OSFClient, nodeID, page string) (*client.Wiki, error) {
	wikis, err := c.ListWikis(ctx, nodeID)
	if err != nil {
		return nil, friendlyWikiError(err, nodeID)
	}
	w, ok := findWikiPage(wikis, page)
	if !ok {
		return nil, fmt.Errorf("wiki page %q not found on project %s (see 'gosf wiki ls %s')", page, nodeID, nodeID)
	}
	return w, nil
}

func init() {
	rootCmd.AddCommand(wikiCmd)
}
