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
	pullDryRun    bool
	pullVersion   int
	pullNoTrack   bool
	pullTrackOnly bool
	pullForce     bool
	pullResolve   string
	pullJobs      int
)

var pullCmd = &cobra.Command{
	Use:   "pull [<project>:<path>] [dest]",
	Short: "Download files from an OSF project",
	Long: `Download files from an OSF project to a local destination.

With no arguments, downloads every tracked file that is missing locally or behind
the remote, from .gosf/gosf.toml. Locally modified files are reported and left
alone unless --force is given. Requires .gosf/gosf.toml with [project].id set
(run 'gosf init').

With a path argument, downloads the specified file or folder and records it
in .gosf/gosf.toml (unless --no-track is set). --track-only registers the files
without transferring any bytes, so a large remote can be adopted and reviewed
before it is downloaded; a plain 'gosf sync' then fetches them.

Path rules follow scp conventions:
  gosf pull abc12:/data/file.csv             → ./data/file.csv
  gosf pull abc12:/data/file.csv out.csv     → ./out.csv
  gosf pull abc12:/data/dir/ local/          → ./local/<files>
  gosf pull abc12:/data/dir  local/          → ./local/dir/<files>
  gosf pull abc12:                           → download entire project`,
	Args:         cobra.RangeArgs(0, 2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateResolve(pullResolve); err != nil {
			return err
		}
		if pullTrackOnly && pullNoTrack {
			return fmt.Errorf("--track-only and --no-track are contradictory: one records entries without downloading, the other downloads without recording")
		}
		if pullTrackOnly && len(args) == 0 {
			return fmt.Errorf("--track-only needs a remote path to register, e.g. gosf pull abc12:/data/ --track-only")
		}
		token := config.LoadToken(flagToken)
		osfClient := client.New(token)
		wb := client.NewWaterbutler(token)

		if len(args) == 0 {
			return runBarePull(cmd.Context(), osfClient, wb, token)
		}
		return runExplicitPull(cmd, args, osfClient, wb)
	},
}

// runBarePull downloads every tracked entry the remote has and local does not.
// Selection is by state, not by any field on the entry: whether a download is
// the right move is a property of how local, the pinned baseline, and the
// remote compare right now (issue #81).
func runBarePull(ctx context.Context, osfClient *client.OSFClient, wb *client.WaterbutlerClient, token string) error {
	manifestPath, repoRoot, err := manifest.FindManifest()
	if manifest.IsNotFound(err) {
		return fmt.Errorf("no .gosf/gosf.toml found — run: gosf init <project-id>")
	}
	if err != nil {
		return err
	}

	m, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}
	if m.Project.ID == "" {
		return fmt.Errorf("no project configured — run: gosf init <project-id>")
	}

	// Per-run cache so the many files sharing directories don't each re-list them.
	res := resolver.New(resolver.NewCachingLister(osfClient))
	jsonMode := flagOutput == "json"
	jsonResults := make([]output.SyncItem, 0)
	manifestChanged := false

	warnUnauthenticated(token, len(m.Files))
	plans, err := scanEntries(ctx, m, repoRoot, res, osfClient, pullJobs, false)
	if err != nil {
		return friendlyAPIError(err, token != "")
	}

	actions := make([]syncAction, len(plans))
	var blocked []string
	for i, p := range plans {
		actions[i] = pullDecision(p.state, pullForce, pullResolve)
		if actions[i] == actionBlocked {
			blocked = append(blocked, divergenceError(*p.entry, p.proj, p.localMD5, p.remoteVersions).Error())
		}
	}
	if len(blocked) > 0 {
		return fmt.Errorf("%s", strings.Join(blocked, "\n\n"))
	}

	deps := transferDeps{res: res, wb: wb, osf: osfClient, showBar: progressBarEnabled(), dryRun: pullDryRun}
	for i, p := range plans {
		action, changed, err := executeEntry(ctx, p, actions[i], deps)
		if err != nil {
			return err
		}
		if changed {
			manifestChanged = true
		}
		if jsonMode {
			jsonResults = append(jsonResults, makeSyncItem(p.entry, p.state, action, p.remoteVersions))
		}
	}

	if manifestChanged && !pullDryRun {
		if err := manifest.Save(m, manifestPath); err != nil {
			return fmt.Errorf("saving manifest: %w", err)
		}
	}
	if jsonMode {
		return output.PrintJSON(os.Stdout, jsonResults)
	}
	return nil
}

// runExplicitPull pulls a specific remote path.
func runExplicitPull(cmd *cobra.Command, args []string, osfClient *client.OSFClient, wb *client.WaterbutlerClient) error {
	target, err := resolver.ParseTarget(args[0])
	if err != nil {
		return err
	}

	dest := "."
	if len(args) == 2 {
		dest = args[1]
	}

	res := resolver.New(osfClient)
	log.Infof("resolving %s:%s", target.NodeID, target.Path)
	items, err := res.ListDir(cmd.Context(), target.NodeID, target.Path)
	if err != nil {
		return friendlyAuthError(err)
	}

	if pullVersion > 0 {
		if len(items) != 1 || items[0].Attributes.Kind != "file" {
			return fmt.Errorf("--version requires a single file target, not a directory")
		}
		versions, err := osfClient.GetFileVersions(cmd.Context(), items[0].ID)
		if err != nil {
			return fmt.Errorf("fetching versions: %w", err)
		}
		if err := validateVersion(versions, pullVersion); err != nil {
			return err
		}
	}

	// Load .gosf/gosf.toml if present for duplicate-tracking check and auto-tracking.
	var m *manifest.Manifest
	var manifestPath string
	manifestCreated := false
	mfPathFound, _, findErr := manifest.FindManifest()
	if findErr == nil {
		if loaded, loadErr := manifest.Load(mfPathFound); loadErr == nil {
			m = loaded
			manifestPath = mfPathFound
		}
	} else if !pullNoTrack && !manifest.IsNotFound(findErr) {
		return findErr
	}

	// If no .gosf/gosf.toml and tracking enabled, we'll create one.
	if m == nil && !pullNoTrack {
		if target.NodeID == "" {
			return fmt.Errorf("no project configured — run: gosf init <project-id>")
		}
		manifestPath = filepath.Join(".gosf", "gosf.toml")
		m = &manifest.Manifest{Project: manifest.ProjectConfig{ID: target.NodeID}}
		manifestCreated = true
	}

	jsonMode := flagOutput == "json"
	s := &pullSession{
		ctx:          cmd.Context(),
		osf:          osfClient,
		wb:           wb,
		result:       output.NewPullResult(pullDryRun),
		jsonMode:     jsonMode,
		dryRun:       pullDryRun,
		version:      pullVersion,
		track:        !pullNoTrack,
		trackOnly:    pullTrackOnly,
		manifest:     m,
		manifestPath: manifestPath,
		nodeID:       target.NodeID,
		remotePath:   target.Path,
	}

	if len(items) == 1 && items[0].Attributes.Kind == "file" {
		item := items[0]
		var destPath string
		switch {
		case dest == ".":
			// Mirror remote path locally (strip leading /).
			destPath = filepath.FromSlash(strings.TrimLeft(target.Path, "/"))
		case func() bool { info, err := os.Stat(dest); return err == nil && info.IsDir() }():
			destPath = filepath.Join(dest, item.Attributes.Name)
		default:
			destPath = dest
		}
		if err := s.file(item, destPath); err != nil {
			return err
		}
	} else {
		if err := s.tree(items, dest, target.Path); err != nil {
			return err
		}
	}

	if s.track && !s.dryRun && len(s.tracked) > 0 {
		for _, te := range s.tracked {
			if findEntryByLocal(s.manifest, te.local) < 0 {
				s.manifest.Files = append(s.manifest.Files, te.entry)
			} else {
				idx := findEntryByLocal(s.manifest, te.local)
				s.manifest.Files[idx] = te.entry
			}
		}
		if err := manifest.Save(s.manifest, s.manifestPath); err != nil {
			return fmt.Errorf("updating .gosf/gosf.toml: %w", err)
		}
		_ = manifestCreated
	}

	if jsonMode {
		return output.PrintJSON(os.Stdout, s.result)
	}
	switch {
	case s.trackOnly:
		log.Infof("registered %d file(s) in %s — run 'gosf sync' to download them", len(s.tracked), s.manifestPath)
	case len(s.result.Downloaded) == 0:
		log.Infof("nothing to download (no files at that path)")
	}
	return nil
}

// trackedEntry pairs a local path with the manifest entry to write.
type trackedEntry struct {
	local string
	entry manifest.Entry
}

// pullSession carries the shared state for one pull invocation.
type pullSession struct {
	ctx          context.Context
	osf          *client.OSFClient
	wb           *client.WaterbutlerClient
	result       *output.PullResult
	jsonMode     bool
	dryRun       bool
	version      int
	track        bool
	trackOnly    bool
	manifest     *manifest.Manifest
	manifestPath string
	nodeID       string
	remotePath   string
	tracked      []trackedEntry
}

// trackedRemoteConflict reports the local path a remote is already tracked
// under when that differs from destPath, or "" when there is no conflict
// (remote untracked, tracked at the same destination, or tracked in a
// different project). A non-empty result means the caller asked to download to
// an explicit alternate destination, so the file should be fetched but not
// (re-)tracked — downloading to a path and tracking that path are distinct.
func trackedRemoteConflict(m *manifest.Manifest, nodeID, remotePath, destPath string) string {
	if m == nil {
		return ""
	}
	idx := findEntryByRemote(m, nodeID, remotePath)
	if idx < 0 {
		return ""
	}
	existing := m.Files[idx].Local
	if existing == destPath || existing == filepath.ToSlash(destPath) {
		return ""
	}
	return existing
}

// file downloads a single file item to destPath, recording it in the result.
func (s *pullSession) file(item client.FileItem, destPath string) error {
	remotePath := item.Attributes.MaterializedPath
	if remotePath == "" {
		remotePath = s.remotePath
	}

	// Decide whether to record this download in the manifest. Downloading to an
	// explicit alternate destination (e.g. a scratch/verification copy) is a
	// plain download: fetch the bytes but leave the existing tracked entry
	// untouched rather than aborting the whole pull.
	trackThis := s.track && s.manifest != nil
	if trackThis {
		if conflict := trackedRemoteConflict(s.manifest, s.nodeID, remotePath, destPath); conflict != "" {
			trackThis = false
			log.Warnf("%q is tracked as %q; downloading to %q without tracking", remotePath, conflict, destPath)
		}
	}

	if s.dryRun {
		s.result.Add(destPath, item.Attributes.Size)
		log.Infof("[dry-run] would download → %s", destPath)
		return nil
	}

	switch {
	case s.trackOnly:
		// Register the file without moving any bytes: the point is to make a
		// large remote visible in the manifest so it can be reviewed (and then
		// fetched by a plain `gosf sync`) rather than downloaded blind.
		log.Infof("+ tracked %s (not downloaded)", destPath)

	// Idempotent skip: if the destination already holds byte-identical content
	// there is nothing to transfer. Only applies when fetching the latest
	// version (item hashes describe the latest, not a requested revision).
	case s.version == 0 && localFileMatches(destPath, item.Attributes.Extra.Hashes.MD5):
		log.Infof("≡ %s (identical, skipped)", destPath)
		s.result.Add(destPath, item.Attributes.Size)

	default:
		if item.Links.Download == "" {
			return fmt.Errorf("no download URL for %q", item.Attributes.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("creating destination directory: %w", err)
		}
		dlURL := item.Links.Download
		if s.version > 0 {
			dlURL = client.RevisionURL(dlURL, s.version)
		}
		if err := s.wb.Download(s.ctx, dlURL, destPath, item.Attributes.Size, progressBarEnabled()); err != nil {
			return err
		}
		log.Infof("↓ %s", destPath)
		s.result.Add(destPath, item.Attributes.Size)
	}

	// Auto-track.
	if trackThis {
		ver, md5 := s.pinFor(item, destPath)
		entryProject := ""
		if s.nodeID != s.manifest.Project.ID {
			entryProject = s.nodeID
		}
		localPath := filepath.ToSlash(destPath)
		s.tracked = append(s.tracked, trackedEntry{
			local: localPath,
			entry: manifest.Entry{
				Local:   localPath,
				Remote:  remotePath,
				Version: ver,
				MD5:     md5,
				Project: entryProject,
			},
		})
	}
	return nil
}

// pinFor returns the version number and MD5 to record for a pulled file. The
// hashes come from OSF's version listing, which is also the only source
// available under --track-only (there is no local copy to hash); falling back to
// the downloaded file covers a remote that reports no hash for the revision.
func (s *pullSession) pinFor(item client.FileItem, destPath string) (version int, md5 string) {
	versions, err := s.osf.GetFileVersions(s.ctx, item.ID)
	if err == nil && len(versions) > 0 {
		if s.version == 0 {
			return versions[0].Number(), versions[0].Attributes.Extra.Hashes.MD5
		}
		for _, v := range versions {
			if v.Number() == s.version {
				md5 = v.Attributes.Extra.Hashes.MD5
				break
			}
		}
	}
	version = s.version
	if md5 == "" && !s.trackOnly {
		md5, _ = computeLocalMD5(destPath)
	}
	return version, md5
}

// tree recursively downloads a slice of items into destDir.
func (s *pullSession) tree(items []client.FileItem, destDir, remoteBase string) error {
	for _, item := range items {
		localPath := filepath.Join(destDir, item.Attributes.Name)

		if item.Attributes.Kind == "folder" {
			children, err := s.osf.ListFilesFromURL(s.ctx, item.Relationships.Files.Links.Related.Href)
			if err != nil {
				return fmt.Errorf("listing %s: %w", item.Attributes.Name, err)
			}
			childRemote := remoteBase + "/" + item.Attributes.Name
			if err := s.tree(children, localPath, childRemote); err != nil {
				return err
			}
			continue
		}

		if err := s.file(item, localPath); err != nil {
			return fmt.Errorf("downloading %s: %w", item.Attributes.Name, err)
		}
	}
	return nil
}

// validateVersion checks that version n exists in the list returned by GetFileVersions.
func validateVersion(versions []client.FileVersion, n int) error {
	for _, v := range versions {
		if v.Number() == n {
			return nil
		}
	}
	if len(versions) == 0 {
		return fmt.Errorf("no versions found for this file")
	}
	return fmt.Errorf("version %d not found; run 'gosf versions <path>' to see available versions", n)
}

func init() {
	pullCmd.Flags().BoolVar(&pullDryRun, "dry-run", false, "Show what would be downloaded without downloading")
	pullCmd.Flags().IntVar(&pullVersion, "version", 0, "Download a specific version number (0 = latest)")
	pullCmd.Flags().BoolVar(&pullNoTrack, "no-track", false, "Download without recording in .gosf/gosf.toml")
	pullCmd.Flags().BoolVar(&pullTrackOnly, "track-only", false, "Record entries in .gosf/gosf.toml without downloading anything")
	pullCmd.Flags().BoolVar(&pullForce, "force", false, "Overwrite locally-modified files with the pinned version")
	pullCmd.Flags().StringVar(&pullResolve, "resolve", "", "Resolve divergence by taking remote: 'theirs'")
	pullCmd.Flags().IntVarP(&pullJobs, "jobs", "j", defaultScanJobs, "Number of files to scan against the remote concurrently")
	rootCmd.AddCommand(pullCmd)
}
