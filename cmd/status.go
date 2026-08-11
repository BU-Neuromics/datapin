package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var (
	statusNoCheckRemote bool
	statusJobs          int
)

var statusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Show sync status of all manifest entries",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		manifestPath, repoRoot, err := manifest.FindManifest()
		if manifest.IsNotFound(err) {
			return fmt.Errorf("no .datapin/datapin.toml found — run 'datapin init <project-id>' to start tracking this repo, then 'datapin add' / 'datapin pull' to register files")
		}
		if err != nil {
			return err
		}

		m, err := manifest.Load(manifestPath)
		if err != nil {
			return err
		}

		token := config.LoadToken(flagToken)
		osfClient := client.New(token)
		// Per-run cache so files sharing directories don't each re-list them.
		res := resolver.New(resolver.NewCachingLister(osfClient))

		if !statusNoCheckRemote {
			warnUnauthenticated(token, len(m.Files)+len(m.Wikis))
		}

		jsonMode := flagOutput == "json"
		allInSync := true

		jsonItems := make([]output.StatusItem, 0)
		var rows [][]output.Cell

		if !statusNoCheckRemote && !jsonMode {
			log.Infof("checking remote")
		}

		// Classify each entry against the remote concurrently, then assemble
		// output in manifest order. Status is read-only: it reports, never mutates.
		type scanResult struct {
			entry          manifest.Entry
			state          manifest.FileState
			remoteVersions []manifest.RemoteVersion
		}
		results := make([]scanResult, len(m.Files))
		g, gctx := errgroup.WithContext(cmd.Context())
		g.SetLimit(scanConcurrency(statusJobs))
		for i := range m.Files {
			i := i
			g.Go(func() error {
				entry := m.Files[i]
				localAbs := filepath.Join(repoRoot, entry.Local)
				localMD5, err := computeLocalMD5(localAbs)
				if err != nil {
					return fmt.Errorf("computing MD5 for %s: %w", entry.Local, err)
				}

				// Fetch remote versions even for unpinned (version=0) entries so an
				// entry that is already byte-identical to the remote is reported as
				// content-in-sync rather than a blanket "never pushed". The scan
				// skips the version history when the listing's latest already
				// settles the classification (see fetchRemoteState).
				var remoteVersions []manifest.RemoteVersion
				if !statusNoCheckRemote {
					proj := entry.ResolveProject(m.Project.ID)
					_, remoteVersions, err = fetchRemoteState(gctx, res, osfClient, proj, entry, localMD5, true)
					if err != nil {
						return err
					}
				}

				results[i] = scanResult{
					entry:          entry,
					state:          manifest.ClassifyFile(entry, localMD5, remoteVersions, statusNoCheckRemote),
					remoteVersions: remoteVersions,
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return friendlyAPIError(err, token != "")
		}

		for _, r := range results {
			if !statusIsInSync(r.state) {
				allInSync = false
			}
			if jsonMode {
				jsonItems = append(jsonItems, buildStatusItem(r.entry, r.state, r.remoteVersions))
			} else {
				statusStr, detail := stateDisplay(r.state, r.entry, r.remoteVersions)
				rows = append(rows, []output.Cell{
					{Text: statusStr, Style: stateStyle(r.state)},
					{Text: r.entry.Local},
					{Text: verLabel(r.entry.Version)},
					{Text: detail, Style: output.Dim},
				})
			}
		}

		// Classify wiki entries (serial — usually a handful; the listing cache
		// collapses repeated per-project fetches).
		if len(m.Wikis) > 0 {
			wcache := newWikiScanCache(osfClient)
			for _, we := range m.Wikis {
				localAbs := filepath.Join(repoRoot, we.Local)
				localMD5, err := wikiLocalMD5(localAbs)
				if err != nil {
					return fmt.Errorf("computing MD5 for %s: %w", we.Local, err)
				}
				var remoteVersions []manifest.RemoteVersion
				if !statusNoCheckRemote {
					proj := we.ResolveProject(m.Project.ID)
					_, remoteVersions, err = fetchWikiRemoteState(cmd.Context(), wcache, osfClient, proj, we, localMD5)
					if err != nil {
						return err
					}
				}
				state := manifest.ClassifyFile(we.BaselineEntry(), localMD5, remoteVersions, statusNoCheckRemote)
				if !statusIsInSync(state) {
					allInSync = false
				}
				if jsonMode {
					jsonItems = append(jsonItems, buildWikiStatusItem(we, state, remoteVersions))
				} else {
					statusStr, detail := stateDisplay(state, we.BaselineEntry(), remoteVersions)
					if detail == "" {
						detail = fmt.Sprintf("wiki %q", we.Page)
					} else {
						detail = fmt.Sprintf("wiki %q — %s", we.Page, detail)
					}
					rows = append(rows, []output.Cell{
						{Text: statusStr, Style: stateStyle(state)},
						{Text: we.Local},
						{Text: verLabel(we.Version)},
						{Text: detail, Style: output.Dim},
					})
				}
			}
		}

		// Classify datasets against their archive remotes (dataset-level
		// rows: one publishable record = one row).
		for i := range m.Datasets {
			ds := &m.Datasets[i]
			state, detail, err := datasetStatus(cmd.Context(), m, repoRoot, ds, statusNoCheckRemote)
			if err != nil {
				return err
			}
			if state != "IN_SYNC" {
				allInSync = false
			}
			if jsonMode {
				jsonItems = append(jsonItems, output.StatusItem{
					Path: ds.Slug, Kind: "dataset", State: state,
					DeclaredVersion: ds.Version,
				})
			} else {
				rows = append(rows, []output.Cell{
					{Text: datasetStateGlyph(state), Style: datasetStateStyle(state)},
					{Text: ds.Slug},
					{Text: verLabel(ds.Version)},
					{Text: detail, Style: output.Dim},
				})
			}
		}

		if jsonMode {
			if err := output.PrintJSON(os.Stdout, jsonItems); err != nil {
				return err
			}
		} else {
			output.RenderTable(os.Stdout, []string{"STATUS", "LOCAL PATH", "VER", "DETAIL"}, rows)
		}

		if !allInSync {
			return &exitCodeError{code: 1}
		}
		return nil
	},
}

// stateStyle maps a file state to the color applied to its status glyph.
// Returns nil (no styling) for states without a distinct color.
func stateStyle(state manifest.FileState) func(string) string {
	switch state {
	case manifest.StateInSync:
		return output.Green
	case manifest.StateMissing:
		return output.Red
	case manifest.StateDivergent:
		return output.RedBold
	case manifest.StateBehind, manifest.StateAheadOfManifest:
		return output.Yellow
	case manifest.StateRemoteNewer:
		return output.Cyan
	case manifest.StatePinOnly, manifest.StateNotPushed:
		return output.Dim
	default:
		return nil
	}
}

// stateDisplay returns the display string and detail for a file state.
func stateDisplay(state manifest.FileState, entry manifest.Entry, remoteVersions []manifest.RemoteVersion) (status, detail string) {
	switch state {
	case manifest.StateInSync:
		return "✓", ""
	case manifest.StateMissing:
		return "✗", "missing locally"
	case manifest.StateBehind:
		latest := latestRemoteVersion(remoteVersions)
		return "BEHIND", fmt.Sprintf("remote is v%d", latest)
	case manifest.StateAheadOfManifest:
		// L ≠ B with R = B: local differs from both the pin and the remote.
		// Whether that is work to publish or a scratch edit to throw away is
		// the user's call, so state the fact and name both remedies.
		return "AHEAD", "locally modified — push to publish, pull --force to discard"
	case manifest.StateRemoteNewer:
		latest := latestRemoteVersion(remoteVersions)
		return "↑", fmt.Sprintf("remote has v%d", latest)
	case manifest.StateNotPushed:
		return "·", "never pushed"
	case manifest.StatePinOnly:
		latest := latestRemoteVersion(remoteVersions)
		return "≡", fmt.Sprintf("identical to remote v%d, unpinned — run sync to pin", latest)
	case manifest.StateDivergent:
		return "DIVERGED", fmt.Sprintf("local and remote both changed since v%d — resolve with --resolve", entry.Version)
	default:
		return "?", ""
	}
}

// statusIsInSync reports whether a state should count as fully in sync for the
// CI-friendly exit code. Only IN_SYNC is fully in sync; PIN_ONLY (content
// matches but the manifest pin is stale) and everything else signal work to do.
func statusIsInSync(state manifest.FileState) bool {
	return state == manifest.StateInSync
}

func verLabel(version int) string {
	if version == 0 {
		return "—"
	}
	return fmt.Sprintf("v%d", version)
}

func init() {
	statusCmd.Flags().BoolVar(&statusNoCheckRemote, "no-check-remote", false, "Skip remote version lookups (faster, but cannot detect BEHIND or REMOTE_NEWER)")
	statusCmd.Flags().IntVarP(&statusJobs, "jobs", "j", defaultScanJobs, "Number of files to check against the remote concurrently")
	rootCmd.AddCommand(statusCmd)
}

func buildStatusItem(entry manifest.Entry, state manifest.FileState, remoteVersions []manifest.RemoteVersion) output.StatusItem {
	item := output.StatusItem{
		Path:            entry.Local,
		Kind:            "file",
		State:           state.String(),
		DeclaredVersion: entry.Version,
	}
	if len(remoteVersions) > 0 {
		item.RemoteLatestVersion = latestRemoteVersion(remoteVersions)
	}
	return item
}

func buildWikiStatusItem(entry manifest.WikiEntry, state manifest.FileState, remoteVersions []manifest.RemoteVersion) output.StatusItem {
	item := output.StatusItem{
		Path:            entry.Local,
		Kind:            "wiki",
		State:           state.String(),
		DeclaredVersion: entry.Version,
	}
	if len(remoteVersions) > 0 {
		item.RemoteLatestVersion = latestRemoteVersion(remoteVersions)
	}
	return item
}
