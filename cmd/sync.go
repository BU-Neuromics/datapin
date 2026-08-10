package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var (
	syncForce         bool
	syncDryRun        bool
	syncNoCheckRemote bool
	syncResolve       string
	syncJobs          int
)

// defaultScanJobs bounds how many manifest entries are classified against the
// remote concurrently during a scan (sync/status pass 1).
const defaultScanJobs = 8

// scanConcurrency clamps a requested job count to a sane minimum of 1.
func scanConcurrency(jobs int) int {
	if jobs < 1 {
		return 1
	}
	return jobs
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync local files with OSF according to .datapin/datapin.toml",
	Long: `Reconcile the files and wiki pages declared in .datapin/datapin.toml with OSF.

Each entry is classified by comparing local content, the pinned baseline, and
the remote, and the one correct action for that state is taken:

  IN_SYNC             nothing
  PIN_ONLY            record the version + md5; no transfer
  MISSING             download it (needs no flag — nothing local is at risk)
  BEHIND              fast-forward to the remote's latest
  REMOTE_NEWER        fast-forward to the remote's latest
  NOT_PUSHED          upload it, when the file exists locally
  AHEAD_OF_MANIFEST   report it and exit non-zero; both publishing and
                      discarding are defensible, so sync picks neither
  DIVERGED            fail before any transfer; pass --resolve to pick a side

Examples:
  datapin sync                         # reconcile everything with a clear answer
  datapin sync --force                 # also discard local edits, restoring from OSF
  datapin sync --resolve=theirs        # resolve diverged entries by taking the remote
  datapin sync --dry-run               # show what would happen
  datapin sync --no-check-remote       # faster, but skips BEHIND/REMOTE_NEWER detection`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateResolve(syncResolve); err != nil {
			return err
		}
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

		if m.Project.ID == "" {
			return fmt.Errorf("no project configured — run: datapin init <project-id>")
		}

		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("sync requires authentication — run 'datapin auth login' or set OSF_TOKEN")
		}

		osfClient := client.New(token)
		wb := client.NewWaterbutler(token)
		// Wrap the lister in a per-run cache so the many files that share
		// directories don't each re-walk (and re-list) the same folders.
		res := resolver.New(resolver.NewCachingLister(osfClient))

		jsonMode := flagOutput == "json"
		showBar := progressBarEnabled()

		jsonResults := make([]output.SyncItem, 0)
		manifestChanged := false

		// Pass 1: classify every entry (no transfers yet). A bounded worker pool
		// overlaps the OSF round-trips, and per-entry progress is logged so a
		// large manifest visibly makes headway instead of appearing stalled.
		plans, err := scanEntries(cmd.Context(), m, repoRoot, res, osfClient, syncJobs, syncNoCheckRemote)
		if err != nil {
			return friendlyAPIError(err, token != "")
		}
		wikiPlans, err := scanWikiEntries(cmd.Context(), m, repoRoot, osfClient, syncNoCheckRemote)
		if err != nil {
			return friendlyAPIError(err, token != "")
		}

		// Decide every entry's action up front so the pre-flight sees the whole
		// run before any bytes move.
		actions := make([]syncAction, len(plans))
		for i, p := range plans {
			actions[i] = syncDecision(p.state, p.localMD5 != "", syncForce, syncResolve)
		}
		wikiActions := make([]syncAction, len(wikiPlans))
		for i, p := range wikiPlans {
			wikiActions[i] = syncDecision(p.state, p.localMD5 != "", syncForce, syncResolve)
		}

		// Pre-flight: refuse to transfer anything if any entry has diverged and
		// no --resolve was given. Fail hard before touching bytes so a bulk sync
		// never applies a partial, half-resolved state.
		var blocked []string
		for i, p := range plans {
			if actions[i] == actionBlocked {
				blocked = append(blocked, divergenceError(*p.entry, p.proj, p.localMD5, p.remoteVersions).Error())
			}
		}
		for i, p := range wikiPlans {
			if wikiActions[i] == actionBlocked {
				blocked = append(blocked, wikiDivergenceError(*p.entry, p.proj, p.localMD5, p.remoteVersions).Error())
			}
		}
		if len(blocked) > 0 {
			return fmt.Errorf("%s", strings.Join(blocked, "\n\n"))
		}

		// Pass 2: execute.
		deps := transferDeps{res: res, wb: wb, osf: osfClient, showBar: showBar, dryRun: syncDryRun}
		reported := 0
		for i, p := range plans {
			if actions[i] == actionReport {
				reported++
			}
			action, changed, perr := executeEntry(cmd.Context(), p, actions[i], deps)
			if perr != nil {
				return perr
			}
			if changed {
				manifestChanged = true
			}
			if jsonMode {
				jsonResults = append(jsonResults, makeSyncItem(p.entry, p.state, action, p.remoteVersions))
			}
		}

		// Execute wiki entries.
		for i, p := range wikiPlans {
			if wikiActions[i] == actionReport {
				reported++
			}
			action, changed, perr := executeWikiEntry(cmd.Context(), osfClient, p, wikiActions[i], syncDryRun)
			if perr != nil {
				return perr
			}
			if changed {
				manifestChanged = true
			}
			if jsonMode {
				jsonResults = append(jsonResults, makeWikiSyncItem(p.entry, p.state, action, p.remoteVersions))
			}
		}

		if manifestChanged && !syncDryRun {
			if err := manifest.Save(m, manifestPath); err != nil {
				return fmt.Errorf("saving manifest: %w", err)
			}
		}

		if jsonMode {
			if err := output.PrintJSON(os.Stdout, jsonResults); err != nil {
				return err
			}
		}

		// Entries that were reported rather than reconciled leave the working
		// tree out of sync, so the run is not a success — but it is not a hard
		// error either: nothing was left half-applied, and both remedies are
		// recoverable. Signal it the way `datapin status` does.
		if reported > 0 {
			return &exitCodeError{code: 1}
		}
		return nil
	},
}

// transferDeps bundles the clients and run-wide switches an entry needs to
// execute its chosen action.
type transferDeps struct {
	res     *resolver.Resolver
	wb      *client.WaterbutlerClient
	osf     *client.OSFClient
	showBar bool
	dryRun  bool
}

// executeEntry carries out one entry's chosen action. Which action that is has
// already been decided by syncDecision/pushDecision/pullDecision; this function
// only performs it, so every command shares identical transfer behaviour.
func executeEntry(ctx context.Context, p entryPlan, act syncAction, d transferDeps) (action string, changed bool, err error) {
	entry := p.entry
	log.Debugf("%s: state=%s action=%s", entry.Local, p.state, act)

	switch act {
	case actionPin:
		return pinEntry(entry, p.remoteVersions, d.dryRun)

	case actionPush:
		return pushFile(ctx, entry, p.proj, p.localAbs, d.res, d.wb, d.showBar, d.dryRun)

	case actionPull:
		item, versions, rerr := resolveForDownload(ctx, p, d)
		if rerr != nil || item == nil {
			log.Warnf("%s: not found on the remote, skipping", entry.Local)
			return "skipped_unresolved", false, nil
		}
		return downloadAndPin(ctx, entry, item, d.wb, p.localAbs, 0, true, versions, d.showBar, d.dryRun, pullActionLabel(p.state))

	case actionRestore:
		item, versions, rerr := resolveForDownload(ctx, p, d)
		if rerr != nil || item == nil {
			log.Warnf("%s: not found on the remote, skipping", entry.Local)
			return "skipped_unresolved", false, nil
		}
		log.Warnf("overwriting locally modified file: %s", entry.Local)
		if entry.Version > 0 {
			return downloadAndPin(ctx, entry, item, d.wb, p.localAbs, entry.Version, false, versions, d.showBar, d.dryRun, "pull_force")
		}
		return downloadAndPin(ctx, entry, item, d.wb, p.localAbs, 0, true, versions, d.showBar, d.dryRun, "pull_force")

	case actionReport:
		// L ≠ B with R = B: local holds work that is on neither the pin nor the
		// remote. Publishing it and discarding it are both defensible, and no
		// hash comparison can tell them apart — so say so and touch nothing.
		log.Warnf("%s: locally modified (differs from pinned v%d and from the remote) — "+
			"'datapin push' to publish it, 'datapin pull --force' to discard it", entry.Local, entry.Version)
		return "skipped_modified", false, nil

	case actionBlocked:
		return "", false, divergenceError(*entry, p.proj, p.localMD5, p.remoteVersions)
	}

	// actionNone: nothing to reconcile.
	switch p.state {
	case manifest.StateInSync:
		log.Debugf("in sync: %s (v%d)", entry.Local, entry.Version)
		return "in_sync", false, nil
	case manifest.StateMissing:
		return "skipped_missing", false, nil
	case manifest.StateNotPushed:
		log.Debugf("%s: nothing local and nothing on the remote", entry.Local)
		return "skipped_not_found", false, nil
	}
	return "noop", false, nil
}

// pullActionLabel names a download for the JSON action_taken field, keeping the
// distinction between restoring a missing file and advancing a stale one.
func pullActionLabel(state manifest.FileState) string {
	switch state {
	case manifest.StateRemoteNewer:
		return "fast_forward"
	case manifest.StateDivergent:
		return "pull_theirs"
	default:
		return "pull"
	}
}

// resolveForDownload supplies the remote file and its versions for a download,
// filling in whatever the scan left out. A --no-check-remote scan resolves
// nothing, and a bare push scan needs no download URL; rather than make every
// caller pre-fetch, the fetch happens here, only for entries that actually
// transfer.
func resolveForDownload(ctx context.Context, p entryPlan, d transferDeps) (*client.FileItem, []manifest.RemoteVersion, error) {
	item := p.resolvedItem
	if item == nil {
		resolved, err := d.res.Resolve(ctx, p.proj, p.entry.Remote)
		if err != nil {
			log.Debugf("%s: not resolved on remote (%v)", p.entry.Remote, err)
			return nil, nil, err
		}
		item = &resolved
	}
	versions := p.remoteVersions
	if len(versions) == 0 {
		if fvs, err := d.osf.GetFileVersions(ctx, item.ID); err == nil {
			versions = fileVersionsToRemote(fvs)
		}
	}
	return item, versions, nil
}

// pinEntry records the latest remote version+MD5 into the manifest entry without
// transferring any bytes (the PIN_ONLY state: local content already matches the
// remote). Returns changed=true unless in dry-run mode.
func pinEntry(entry *manifest.Entry, remoteVersions []manifest.RemoteVersion, dryRun bool) (string, bool, error) {
	latest := latestRemoteVersionInfo(remoteVersions)
	if dryRun {
		log.Infof("[dry-run] %s: identical to remote v%d, would pin", entry.Local, latest.Version)
		return "pin", false, nil
	}
	entry.Version = latest.Version
	entry.MD5 = latest.MD5
	log.Infof("≡ %s: identical to remote v%d, pinned (no transfer)", entry.Local, latest.Version)
	return "pinned", true, nil
}

// pushFile uploads the local file and updates the entry. It chooses between
// creating a new file and PUTting a new version by whether the remote file
// actually exists — not by entry.Version. A version=0 entry whose remote file
// already exists must update the existing file (create would 409); this is the
// fix for #62. The (cached) resolver makes the existence check cheap: the scan
// already listed the parent directory.
func pushFile(
	ctx context.Context,
	entry *manifest.Entry,
	proj, localAbs string,
	res *resolver.Resolver,
	wb *client.WaterbutlerClient,
	showBar, dryRun bool,
) (action string, changed bool, err error) {
	oldVer := entry.Version

	uploadURL, remoteExists, err := uploadTarget(ctx, entry, proj, res)
	if err != nil {
		return "", false, fmt.Errorf("resolving upload URL for %s: %w", entry.Local, err)
	}

	if dryRun {
		logPush(true, entry.Local, remoteExists, oldVer, oldVer+1)
		return "push", false, nil
	}

	log.Debugf("uploading %s → %s", entry.Local, uploadURL)
	result, uploadErr := wb.Upload(ctx, localAbs, uploadURL, showBar)
	if uploadErr != nil {
		return "", false, fmt.Errorf("uploading %s: %w", entry.Local, uploadErr)
	}

	newVer := result.Version
	if newVer == 0 {
		newVer = oldVer + 1
	}
	logPush(false, entry.Local, remoteExists, oldVer, newVer)

	entry.Version = newVer
	entry.MD5 = result.MD5
	return "push", true, nil
}

// logPush emits the per-file push activity line, distinguishing a first push
// (no remote file) from a new version of an existing file, and noting when the
// prior local pin was unknown (oldVer == 0 but the remote already existed).
func logPush(dryRun bool, local string, remoteExists bool, oldVer, newVer int) {
	prefix, verb := "", "pushed"
	if dryRun {
		prefix, verb = "[dry-run] ", "would push"
	}
	switch {
	case !remoteExists:
		log.Infof("%s↑ %s %s (first push → v%d)", prefix, verb, local, newVer)
	case oldVer > 0:
		log.Infof("%s↑ %s %s v%d → v%d", prefix, verb, local, oldVer, newVer)
	default:
		log.Infof("%s↑ %s %s (new version → v%d)", prefix, verb, local, newVer)
	}
}

// uploadTarget returns the Waterbutler upload URL for an entry and whether the
// remote file already exists. An existing file's own upload link creates a new
// version on PUT; a new file uses the parent folder's ID-based create URL.
func uploadTarget(ctx context.Context, entry *manifest.Entry, proj string, res *resolver.Resolver) (url string, remoteExists bool, err error) {
	if item, rerr := res.Resolve(ctx, proj, entry.Remote); rerr == nil && item.Attributes.Kind == "file" {
		return item.Links.Upload, true, nil
	}
	// New file — resolve the parent folder's ID-based upload base (or root).
	parentDir := filepath.Dir(entry.Remote)
	filename := filepath.Base(entry.Remote)
	base, err := folderUploadBase(ctx, res, proj, parentDir)
	if err != nil {
		return "", false, err
	}
	return client.AppendUploadName(base, filename), false, nil
}

// downloadAndPin downloads a file version to localAbs and optionally updates the
// manifest pin. When toLatest is true it fetches the latest version (no revision
// query) and re-pins the entry to the latest version+MD5; otherwise it fetches
// the given revision and leaves the pin unchanged (baseline restore), verifying
// the downloaded MD5 against the pin.
func downloadAndPin(
	ctx context.Context,
	entry *manifest.Entry,
	item *client.FileItem,
	wb *client.WaterbutlerClient,
	localAbs string,
	revision int,
	toLatest bool,
	remoteVersions []manifest.RemoteVersion,
	showBar, dryRun bool,
	action string,
) (string, bool, error) {
	latest := latestRemoteVersionInfo(remoteVersions)
	label := fmt.Sprintf("v%d", entry.Version)
	if toLatest {
		label = fmt.Sprintf("→ v%d", latest.Version)
	}
	if dryRun {
		log.Infof("[dry-run] ↓ %s (%s)", entry.Local, label)
		return action, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(localAbs), 0755); err != nil {
		return "", false, err
	}
	dlURL := item.Links.Download
	if !toLatest && revision > 0 {
		dlURL = client.RevisionURL(dlURL, revision)
	}
	log.Debugf("downloading %s (%s)", entry.Local, label)
	if err := wb.Download(ctx, dlURL, localAbs, -1, showBar); err != nil {
		return "", false, fmt.Errorf("downloading %s: %w", entry.Local, err)
	}

	gotMD5, _ := computeLocalMD5(localAbs)
	if toLatest {
		entry.Version = latest.Version
		if latest.MD5 != "" {
			entry.MD5 = latest.MD5
		} else {
			entry.MD5 = gotMD5
		}
	} else if entry.MD5 != "" && gotMD5 != entry.MD5 {
		os.Remove(localAbs)
		return "", false, fmt.Errorf("MD5 mismatch after downloading %s: expected %s, got %s", entry.Local, entry.MD5, gotMD5)
	}
	log.Infof("↓ pulled %s (%s)", entry.Local, label)
	return action, true, nil
}

func makeSyncItem(entry *manifest.Entry, state manifest.FileState, action string, remoteVersions []manifest.RemoteVersion) output.SyncItem {
	item := output.SyncItem{
		Path:            entry.Local,
		Kind:            "file",
		State:           state.String(),
		DeclaredVersion: entry.Version,
		ActionTaken:     action,
	}
	if len(remoteVersions) > 0 {
		item.RemoteLatestVersion = latestRemoteVersion(remoteVersions)
	}
	return item
}

func makeWikiSyncItem(entry *manifest.WikiEntry, state manifest.FileState, action string, remoteVersions []manifest.RemoteVersion) output.SyncItem {
	item := output.SyncItem{
		Path:            entry.Local,
		Kind:            "wiki",
		State:           state.String(),
		DeclaredVersion: entry.Version,
		ActionTaken:     action,
	}
	if len(remoteVersions) > 0 {
		item.RemoteLatestVersion = latestRemoteVersion(remoteVersions)
	}
	return item
}

func init() {
	syncCmd.Flags().BoolVar(&syncForce, "force", false, "Discard local modifications, restoring the tracked version from OSF")
	syncCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Show what would happen without making changes")
	syncCmd.Flags().BoolVar(&syncNoCheckRemote, "no-check-remote", false, "Skip remote version lookups (faster, cannot detect BEHIND/REMOTE_NEWER)")
	syncCmd.Flags().StringVar(&syncResolve, "resolve", "", "Resolve divergence: 'ours' (keep local) or 'theirs' (keep remote)")
	syncCmd.Flags().IntVarP(&syncJobs, "jobs", "j", defaultScanJobs, "Number of files to scan against the remote concurrently")
	rootCmd.AddCommand(syncCmd)
}
