package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/site"
)

var (
	siteOut         string
	sitePreviewAddr string
	siteGitHubToken string
)

var siteCmd = &cobra.Command{
	Use:   "site",
	Short: "Build, preview, and publish the project's documentation site",
	Long: `Generate a static documentation site from the manifest: markdown
pages under [[site.pages]] plus a citation-ready landing page per dataset
(DOI, checksums, schema.org JSON-LD). Deploys as a single orphan commit
force-pushed to the gh-pages branch — no workflow file, no site history.`,
}

var siteBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Render the site into a local directory",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, outDir, err := buildSite()
		if err != nil {
			return err
		}
		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, map[string]any{"dir": outDir})
		}
		log.Infof("site built into %s", outDir)
		return nil
	},
}

var sitePreviewCmd = &cobra.Command{
	Use:   "preview",
	Short: "Build the site and serve it locally",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, outDir, err := buildSite()
		if err != nil {
			return err
		}
		srv := &http.Server{
			Addr:              sitePreviewAddr,
			Handler:           http.FileServer(http.Dir(outDir)),
			ReadHeaderTimeout: 5 * time.Second,
		}
		log.Infof("serving %s at http://%s (Ctrl-C to stop)", outDir, sitePreviewAddr)
		errCh := make(chan error, 1)
		go func() { errCh <- srv.ListenAndServe() }()
		select {
		case <-cmd.Context().Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
			return nil
		case err := <-errCh:
			return err
		}
	},
}

var sitePublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Build the site and force-push it to gh-pages",
	Long: `Build the site and publish it as a single orphan commit on the
gh-pages branch. The remote comes from [site].repo (owner/repo on
github.com) or, absent that, the enclosing repository's origin.

On the first publish, GitHub Pages is enabled via the REST API when a
token is available (--github-token, GITHUB_TOKEN, GH_TOKEN, or
'gh auth token'). Without a token the push relies on your git credential
helper and the Pages toggle is skipped with a note.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		m, _, outDir, err := buildSite()
		if err != nil {
			return err
		}

		remoteURL, repoSlug, err := siteRemote(m)
		if err != nil {
			return err
		}
		token := resolveGitHubToken(cmd.Context(), siteGitHubToken)

		log.Infof("publishing site to %s (gh-pages)", remoteURL)
		if err := site.DeployGHPages(cmd.Context(), outDir, remoteURL, token); err != nil {
			return err
		}

		pagesEnabled := false
		if repoSlug != "" && token != "" {
			if err := site.EnablePages(cmd.Context(), os.Getenv("DATAPIN_GITHUB_API"), repoSlug, token); err != nil {
				log.Warnf("site pushed, but enabling GitHub Pages failed: %v — enable it once in the repo settings", err)
			} else {
				pagesEnabled = true
			}
		} else if repoSlug != "" {
			log.Infof("no GitHub token found — if this is the first publish, enable Pages (gh-pages branch) once in the repo settings")
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, map[string]any{
				"remote": remoteURL, "branch": "gh-pages", "pages_enabled": pagesEnabled,
			})
		}
		log.Infof("✓ site published")
		return nil
	},
}

// buildSite loads the manifest, gathers inputs, renders, and writes the
// site. Returns the manifest, repo root, and output directory.
func buildSite() (*manifest.Manifest, string, string, error) {
	manifestPath, repoRoot, err := manifest.FindManifest()
	if err != nil {
		return nil, "", "", err
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return nil, "", "", err
	}
	if len(m.Site.Pages) == 0 && len(m.Datasets) == 0 {
		return nil, "", "", fmt.Errorf("nothing to build — add [[site.pages]] or [[datasets]] to the manifest")
	}

	in := site.BuildInput{
		Manifest:    m,
		PageSources: map[string][]byte{},
		FileSizes:   map[string]int64{},
		Year:        fmt.Sprintf("%d", time.Now().Year()),
	}
	for _, p := range m.Site.Pages {
		data, err := os.ReadFile(filepath.Join(repoRoot, p.Local))
		if err != nil {
			return nil, "", "", fmt.Errorf("site page %s: %w", p.Local, err)
		}
		in.PageSources[p.Local] = data
	}
	for i := range m.Datasets {
		_, sizes, _, err := datasetLocalState(repoRoot, &m.Datasets[i])
		if err != nil {
			return nil, "", "", err
		}
		for local, sz := range sizes {
			in.FileSizes[local] = sz
		}
	}

	built, err := site.Build(in)
	if err != nil {
		return nil, "", "", err
	}
	outDir := siteOut
	if outDir == "" {
		outDir = filepath.Join(repoRoot, "public")
	}
	if err := site.WriteDir(built, outDir); err != nil {
		return nil, "", "", err
	}
	return m, repoRoot, outDir, nil
}

// siteRemote resolves the push target: [site].repo → github.com URL,
// else the enclosing repo's origin. repoSlug is "owner/repo" when known
// (for the Pages API), "" otherwise.
func siteRemote(m *manifest.Manifest) (remoteURL, repoSlug string, err error) {
	if m.Site.Repo != "" {
		return "https://github.com/" + m.Site.Repo + ".git", m.Site.Repo, nil
	}
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", fmt.Errorf("no [site].repo configured and no git origin found — set repo = \"owner/repo\" under [site]")
	}
	url := strings.TrimSpace(string(out))
	if slug, ok := githubSlug(url); ok {
		return url, slug, nil
	}
	return url, "", nil
}

// githubSlug extracts "owner/repo" from a github.com remote URL.
func githubSlug(url string) (string, bool) {
	for _, prefix := range []string{"https://github.com/", "git@github.com:"} {
		if rest, ok := strings.CutPrefix(url, prefix); ok {
			return strings.TrimSuffix(rest, ".git"), true
		}
	}
	return "", false
}

// resolveGitHubToken walks the plan §2.6 ladder:
// --github-token > GITHUB_TOKEN > GH_TOKEN > `gh auth token`.
func resolveGitHubToken(ctx context.Context, flag string) string {
	if flag != "" {
		return flag
	}
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if t := os.Getenv(env); t != "" {
			return t
		}
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func init() {
	siteBuildCmd.Flags().StringVar(&siteOut, "out", "", "Output directory (default: <repo>/public)")
	sitePreviewCmd.Flags().StringVar(&siteOut, "out", "", "Output directory (default: <repo>/public)")
	sitePreviewCmd.Flags().StringVar(&sitePreviewAddr, "addr", "localhost:8383", "Address to serve the preview on")
	sitePublishCmd.Flags().StringVar(&siteOut, "out", "", "Output directory (default: <repo>/public)")
	sitePublishCmd.Flags().StringVar(&siteGitHubToken, "github-token", "", "GitHub token for the gh-pages push and Pages enablement")
	siteCmd.AddCommand(siteBuildCmd, sitePreviewCmd, sitePublishCmd)
	rootCmd.AddCommand(siteCmd)
}
