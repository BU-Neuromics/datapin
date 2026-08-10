package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/gitutil"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/picker"
)

var (
	onboardProject    string
	onboardRemoteBase string
)

var onboardCmd = &cobra.Command{
	Use:   "onboard",
	Short: "Guided setup: attach a project and pick local files to push",
	Long: `Walk through gosf setup interactively: authenticate, attach an OSF
project, and pick the local files (things git doesn't track) to push.

onboard is resumable — it detects the current state and starts at the right
step, so it's safe to re-run as you add more files. It stops after recording
your selections in .gosf/gosf.toml; run 'gosf sync' to push.

Requires an interactive terminal. For scripting, use 'gosf init', 'gosf add',
and 'gosf sync' directly.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runOnboard,
}

func runOnboard(cmd *cobra.Command, args []string) error {
	if flagOutput == "json" {
		return errors.New("onboard is interactive and unavailable with --output=json; use gosf init / add / sync")
	}
	if !isInteractive() {
		return errors.New("onboard needs an interactive terminal; use gosf init / add / sync for scripting")
	}
	ctx := cmd.Context()

	// Phase 0 — Auth (offered, not required).
	token := config.LoadToken(flagToken)
	if token == "" {
		fmt.Fprintln(os.Stderr, output.Bold("Authenticate"))
		if confirm("Log in to OSF now? (needed to browse your projects and to push later)") {
			if err := runLogin(ctx, false); err != nil {
				return err
			}
			token = config.LoadToken(flagToken)
		} else {
			fmt.Fprintln(os.Stderr, output.Dim("Continuing unauthenticated — enter a GUID manually; log in before 'gosf sync'."))
		}
	} else {
		fmt.Fprintln(os.Stderr, output.Green("✓")+" authenticated")
	}
	osfClient := client.New(token)

	// Phase 1 — ensure a manifest with a project id.
	m, mfPath, repoRoot, err := ensureManifest(ctx, osfClient, token)
	if err != nil {
		return err
	}

	// Phase 2 — select local files to push.
	fmt.Fprintln(os.Stderr, output.Bold("\nSelect files to push"))
	cands, err := gitutil.Candidates(repoRoot)
	if err != nil {
		return fmt.Errorf("scanning local files: %w", err)
	}
	cands = untrackedCandidates(cands, m)
	if len(cands) == 0 {
		fmt.Fprintln(os.Stderr, output.Dim("No new untracked files to add."))
		return onboardSummary(mfPath)
	}

	selected, err := picker.Run(cands)
	if errors.Is(err, picker.ErrCanceled) {
		fmt.Fprintln(os.Stderr, "Canceled — nothing added.")
		return nil
	}
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		fmt.Fprintln(os.Stderr, output.Dim("No files selected."))
		return onboardSummary(mfPath)
	}

	base := onboardRemoteBase
	if base == "" {
		base = promptLine("Remote base path in OSF Storage", "/")
	}

	added := 0
	for _, rel := range selected {
		if findEntryByLocal(m, rel) >= 0 {
			continue
		}
		m.Files = append(m.Files, manifest.Entry{
			Local:  rel,
			Remote: remotePath(base, rel),
		})
		added++
	}
	if added > 0 {
		if err := manifest.Save(m, mfPath); err != nil {
			return fmt.Errorf("updating %s: %w", mfPath, err)
		}
	}
	fmt.Fprintf(os.Stderr, "%s added %d file(s) to %s\n", output.Green("✓"), added, mfPath)
	return onboardSummary(mfPath)
}

// ensureManifest loads (or creates) the manifest and guarantees a project id,
// prompting to choose one when needed.
func ensureManifest(ctx context.Context, c *client.OSFClient, token string) (*manifest.Manifest, string, string, error) {
	mfPath, repoRoot, findErr := manifest.FindManifest()
	if findErr != nil && !manifest.IsNotFound(findErr) {
		return nil, "", "", findErr
	}

	if manifest.IsNotFound(findErr) {
		fmt.Fprintln(os.Stderr, output.Bold("\nChoose a project"))
		proj, err := chooseProject(ctx, c, token)
		if err != nil {
			return nil, "", "", err
		}
		p, _, err := manifest.Init(".", proj)
		if err != nil {
			return nil, "", "", err
		}
		m, err := manifest.Load(p)
		if err != nil {
			return nil, "", "", err
		}
		cwd, _ := os.Getwd()
		fmt.Fprintf(os.Stderr, "%s created %s (project %s)\n", output.Green("✓"), p, proj)
		return m, p, cwd, nil
	}

	m, err := manifest.Load(mfPath)
	if err != nil {
		return nil, "", "", err
	}
	if m.Project.ID == "" {
		fmt.Fprintln(os.Stderr, output.Bold("\nChoose a project"))
		proj, err := chooseProject(ctx, c, token)
		if err != nil {
			return nil, "", "", err
		}
		m.Project.ID = proj
		if err := manifest.Save(m, mfPath); err != nil {
			return nil, "", "", err
		}
	}
	fmt.Fprintf(os.Stderr, "%s using %s (project %s)\n", output.Green("✓"), mfPath, m.Project.ID)
	return m, mfPath, repoRoot, nil
}

// chooseProject resolves a project GUID: the --project flag, else a numbered
// pick from the user's projects (when authenticated), else a typed GUID.
func chooseProject(ctx context.Context, c *client.OSFClient, token string) (string, error) {
	if onboardProject != "" {
		return onboardProject, nil
	}
	if token != "" {
		if nodes, err := c.GetUserNodes(ctx); err == nil && len(nodes) > 0 {
			fmt.Fprintln(os.Stderr, "Your projects:")
			for i, n := range nodes {
				vis := "private"
				if n.Attributes.Public {
					vis = "public"
				}
				fmt.Fprintf(os.Stderr, "  %2d) %s  (%s, %s)\n", i+1, n.Attributes.Title, n.ID, vis)
			}
			ans := promptLine("Pick a number, or type a project GUID", "")
			if idx, convErr := strconv.Atoi(ans); convErr == nil && idx >= 1 && idx <= len(nodes) {
				return nodes[idx-1].ID, nil
			}
			if ans != "" {
				return ans, nil
			}
			return "", errors.New("no project selected")
		}
	}
	guid := promptLine("Enter your OSF project GUID (the 5-char id in the osf.io URL)", "")
	if guid == "" {
		return "", errors.New("no project GUID provided")
	}
	return guid, nil
}

func onboardSummary(mfPath string) error {
	fmt.Fprintln(os.Stderr, output.Bold("\nNext steps"))
	fmt.Fprintf(os.Stderr, "  review:  %s\n", mfPath)
	fmt.Fprintln(os.Stderr, "  status:  gosf status")
	fmt.Fprintln(os.Stderr, "  push:    gosf sync")
	return nil
}

// untrackedCandidates drops candidates already recorded in the manifest.
func untrackedCandidates(cands []gitutil.Candidate, m *manifest.Manifest) []gitutil.Candidate {
	tracked := map[string]bool{}
	if m != nil {
		for _, e := range m.Files {
			tracked[filepath.ToSlash(e.Local)] = true
		}
	}
	var out []gitutil.Candidate
	for _, c := range cands {
		if !tracked[c.Path] {
			out = append(out, c)
		}
	}
	return out
}

// remotePath maps a repo-relative path to a remote OSF path under base.
func remotePath(base, rel string) string {
	base = "/" + strings.Trim(base, "/")
	rel = strings.TrimLeft(filepath.ToSlash(rel), "/")
	if base == "/" {
		return "/" + rel
	}
	return base + "/" + rel
}

// promptLine reads a trimmed line from stdin, returning def on empty input.
func promptLine(prompt, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", prompt)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		if ans := strings.TrimSpace(scanner.Text()); ans != "" {
			return ans
		}
	}
	return def
}

func init() {
	onboardCmd.Flags().StringVar(&onboardProject, "project", "", "Project GUID to attach (skips the project prompt)")
	onboardCmd.Flags().StringVar(&onboardRemoteBase, "remote-base", "", "Remote base path for pushed files (skips the prompt)")
	rootCmd.AddCommand(onboardCmd)
}
