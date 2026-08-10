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
	pushDryRun        bool
	pushConflict      string
	pushNoTrack       bool
	pushNoCheckRemote bool
	pushForce         bool
	pushYes           bool
	pushResolve       string
	pushJobs          int
)

var pushCmd = &cobra.Command{
	Use:   "push [<src> <project>:<path>]",
	Short: "Upload a file or directory to an OSF project",
	Long: `Upload a local file or directory to an OSF project.

With no arguments, publishes every tracked file that holds local work the remote
does not have: files modified since they were last synced, and files never
pushed. Requires .gosf/gosf.toml with [project].id set (run 'gosf init').

With arguments, uploads the specified local file or directory and records it
in .gosf/gosf.toml (unless --no-track is set).

Conflict behaviour (--conflict):
  skip      (default) Skip files that already exist at the destination.
  overwrite           Replace existing files.
  rename              Append _1, _2, … to find a free name.

Examples:
  gosf push                               # publish local changes to tracked files
  gosf push results.csv abc12:/data/results.csv
  gosf push ./results/  abc12:/data/
  gosf push data.csv    abc12:/data/data.csv --conflict=overwrite`,
	Args:         cobra.RangeArgs(0, 2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateResolve(pushResolve); err != nil {
			return err
		}
		if len(args) == 0 {
			return runBarePush(cmd)
		}
		if len(args) == 1 {
			return fmt.Errorf("usage: gosf push <src> <project>:<path>")
		}

		src := args[0]
		cutset := "/"
		if string(filepath.Separator) != "/" {
			cutset += string(filepath.Separator)
		}
		src = strings.TrimRight(src, cutset)

		target, err := resolver.ParseTarget(args[1])
		if err != nil {
			return err
		}

		switch pushConflict {
		case "skip", "overwrite", "rename":
		default:
			return fmt.Errorf("--conflict must be skip, overwrite, or rename; got %q", pushConflict)
		}

		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("push requires authentication — run 'gosf auth login' or set OSF_TOKEN")
		}

		osfClient := client.New(token)

		srcInfo, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("source: %w", err)
		}

		// Load existing .gosf/gosf.toml; if absent and tracking enabled, prepare to create one.
		var mf *manifest.Manifest
		var mfPath string
		mfPathFound, _, findErr := manifest.FindManifest()
		if findErr == nil {
			if loaded, loadErr := manifest.Load(mfPathFound); loadErr == nil {
				mf = loaded
				mfPath = mfPathFound
			}
		} else if manifest.IsNotFound(findErr) && !pushNoTrack {
			mfPath = filepath.Join(".gosf", "gosf.toml")
			mf = &manifest.Manifest{Project: manifest.ProjectConfig{ID: target.NodeID}}
		}

		jsonMode := flagOutput == "json"
		s := &pushSession{
			ctx:          cmd.Context(),
			res:          resolver.New(osfClient),
			wb:           client.NewWaterbutler(token),
			result:       output.NewPushResult(pushDryRun),
			jsonMode:     jsonMode,
			dryRun:       pushDryRun,
			conflict:     pushConflict,
			manifest:     mf,
			manifestPath: mfPath,
			track:        !pushNoTrack && mf != nil,
			nodeID:       target.NodeID,
		}

		if srcInfo.IsDir() {
			err = s.dir(src, target.NodeID, target.Path)
		} else {
			err = s.file(src, target.NodeID, target.Path)
		}
		if err != nil {
			return err
		}

		if s.manifestDirty && !s.dryRun {
			if err := manifest.Save(s.manifest, s.manifestPath); err != nil {
				return fmt.Errorf("updating .gosf/gosf.toml: %w", err)
			}
		}

		if jsonMode {
			return output.PrintJSON(os.Stdout, s.result)
		}
		if len(s.result.Uploaded) == 0 {
			log.Infof("nothing to upload (no files at source)")
		}
		return nil
	},
}

// runBarePush pushes all push-eligible manifest entries.
func runBarePush(cmd *cobra.Command) error {
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

	token := config.LoadToken(flagToken)
	if token == "" {
		return fmt.Errorf("push requires authentication — run 'gosf auth login' or set OSF_TOKEN")
	}

	osfClient := client.New(token)
	wb := client.NewWaterbutler(token)
	// Per-run cache so the many files sharing directories don't each re-list them.
	res := resolver.New(resolver.NewCachingLister(osfClient))

	jsonMode := flagOutput == "json"
	quiet := flagQuiet || jsonMode
	jsonResults := make([]output.SyncItem, 0)
	manifestChanged := false

	// Pass 1: classify every entry (no transfers yet), then keep the ones that
	// hold local work to publish. Selection is by state, not by any field on the
	// entry: what a push should do is a property of how local, the pinned
	// baseline, and the remote compare right now (issue #81).
	scanned, err := scanEntries(cmd.Context(), m, repoRoot, res, osfClient, pushJobs, pushNoCheckRemote)
	if err != nil {
		return friendlyAPIError(err, token != "")
	}
	plans := make([]entryPlan, 0, len(scanned))
	actions := make([]syncAction, 0, len(scanned))
	for _, p := range scanned {
		act := pushDecision(p.state, p.localMD5 != "", pushForce, pushResolve)
		if act == actionNone {
			continue
		}
		plans = append(plans, p)
		actions = append(actions, act)
	}

	// Pre-flight: divergence fails before any prompt or transfer.
	if err := preflightPush(plans, actions); err != nil {
		return err
	}

	// Confirmation gate. A push that writes remote bytes must be confirmed
	// unless --yes/--force (or --quiet). In JSON mode --force is mandatory so a
	// non-interactive run can never hang on a prompt (same rule as `gosf rm`).
	states := make([]manifest.FileState, len(plans))
	for i, p := range plans {
		states[i] = p.state
	}
	if needsPushConfirmation(states) && !pushYes && !pushForce && !pushDryRun {
		if jsonMode {
			return fmt.Errorf("push in --output=json mode requires --force (no interactive confirmation available)")
		}
		node, _ := osfClient.GetNode(cmd.Context(), m.Project.ID)
		printPushPlan(os.Stderr, node, m.Project.ID, plans, states)
		if !isInteractive() {
			return fmt.Errorf("refusing to push without confirmation (no TTY); pass --yes to confirm or --force")
		}
		if !confirm("Proceed with push?") {
			log.Warnf("aborted")
			return nil
		}
	} else if !quiet && !jsonMode && len(plans) > 0 {
		node, _ := osfClient.GetNode(cmd.Context(), m.Project.ID)
		printPushPlan(os.Stderr, node, m.Project.ID, plans, states)
	} else if len(plans) == 0 {
		// Selection is by state, so an empty plan is the normal "everything is
		// already published" outcome — say so rather than printing a plan for
		// nothing (and skip the node fetch it would need).
		log.Infof("nothing to push — no tracked file has local changes to publish")
	}

	// Pass 2: execute.
	deps := transferDeps{res: res, wb: wb, osf: osfClient, showBar: progressBarEnabled(), dryRun: pushDryRun}
	for i, p := range plans {
		action, changed, err := executeEntry(cmd.Context(), p, actions[i], deps)
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

	if manifestChanged && !pushDryRun {
		if err := manifest.Save(m, manifestPath); err != nil {
			return fmt.Errorf("saving manifest: %w", err)
		}
	}
	if jsonMode {
		return output.PrintJSON(os.Stdout, jsonResults)
	}
	return nil
}

// preflightPush fails hard on any entry that has diverged without a --resolve,
// before any transfer or prompt, so a bulk push never applies a partial,
// half-resolved state.
func preflightPush(plans []entryPlan, actions []syncAction) error {
	var blocked []string
	for i, p := range plans {
		if actions[i] == actionBlocked {
			blocked = append(blocked, divergenceError(*p.entry, p.proj, p.localMD5, p.remoteVersions).Error())
		}
	}
	if len(blocked) > 0 {
		return fmt.Errorf("%s", strings.Join(blocked, "\n\n"))
	}
	return nil
}

// pushSession carries the shared state for one push invocation.
type pushSession struct {
	ctx           context.Context
	res           *resolver.Resolver
	wb            *client.WaterbutlerClient
	result        *output.PushResult
	jsonMode      bool
	dryRun        bool
	conflict      string
	manifest      *manifest.Manifest
	manifestPath  string
	track         bool
	nodeID        string
	manifestDirty bool
}

// file uploads a single local file to an OSF destination path.
func (s *pushSession) file(srcPath, nodeID, destPath string) error {
	parentDir, filename := deriveUploadTarget(destPath, srcPath)

	existing, err := s.res.ListDir(s.ctx, nodeID, parentDir)
	if err != nil {
		return fmt.Errorf("destination folder %s is not accessible (does it exist?): %w", parentDir, err)
	}

	var existingItem *client.FileItem
	for i := range existing {
		if existing[i].Attributes.Name == filename && existing[i].Attributes.Kind == "file" {
			existingItem = &existing[i]
			break
		}
	}

	// osfstorage addresses folders by opaque ID, so a new-file upload must target
	// the parent folder's ID-based links.upload (or the storage root), never a
	// name-built path.
	uploadBase, err := folderUploadBase(s.ctx, s.res, nodeID, parentDir)
	if err != nil {
		return err
	}

	plan, err := planUpload(s.conflict, existingItem, uploadBase, filename, existing)
	if err != nil {
		return err
	}
	destFull := strings.TrimRight(parentDir, "/") + "/" + plan.name

	// Refuse if a different local path already tracks this remote destination.
	if s.track && s.manifest != nil && plan.action != "skip" {
		if idx := findEntryByRemote(s.manifest, nodeID, destFull); idx >= 0 {
			if s.manifest.Files[idx].Local != srcPath {
				return fmt.Errorf("push refused: %s:%s is already tracked to %q — edit .gosf/gosf.toml to change",
					nodeID, destFull, s.manifest.Files[idx].Local)
			}
		}
	}

	// Idempotent overwrite: if the target already holds identical bytes, skip
	// rather than mint a redundant remote version.
	if existingItem != nil && plan.action == "overwrite" {
		localMD5, _ := computeLocalMD5(srcPath)
		if redundantOverwrite(plan.action, localMD5, existingItem.Attributes.Extra.Hashes.MD5) {
			s.result.Add(destFull, "skip")
			log.Infof("≡ %s (identical, skipped)", destFull)
			return nil
		}
	}

	switch {
	case plan.action == "skip":
		s.result.Add(destFull, "skip")
		log.Infof("skip %s (already exists)", destFull)
		return nil
	case s.dryRun:
		s.result.Add(destFull, plan.action)
		log.Infof("[dry-run] would %s → %s", plan.action, destFull)
		return nil
	}

	if plan.action == "overwrite" {
		log.Infof("↑ %s → %s (new version)", srcPath, destFull)
	} else {
		log.Infof("↑ %s → %s (%s)", srcPath, destFull, plan.action)
	}
	uploadResult, err := s.wb.Upload(s.ctx, srcPath, plan.url, progressBarEnabled())
	if err != nil {
		return err
	}
	s.result.Add(destFull, plan.action)

	// Auto-track: create or update the manifest entry.
	if s.track && s.manifest != nil && uploadResult.Version > 0 {
		if idx := findEntryByLocal(s.manifest, srcPath); idx >= 0 {
			e := &s.manifest.Files[idx]
			e.Version = uploadResult.Version
			e.MD5 = uploadResult.MD5
			s.manifestDirty = true
		} else if idx := findEntryByRemote(s.manifest, nodeID, destFull); idx >= 0 {
			e := &s.manifest.Files[idx]
			e.Version = uploadResult.Version
			e.MD5 = uploadResult.MD5
			s.manifestDirty = true
		} else {
			entryProject := ""
			if nodeID != s.manifest.Project.ID {
				entryProject = nodeID
			}
			s.manifest.Files = append(s.manifest.Files, manifest.Entry{
				Local:   srcPath,
				Remote:  destFull,
				Version: uploadResult.Version,
				MD5:     uploadResult.MD5,
				Project: entryProject,
			})
			s.manifestDirty = true
		}
	}
	return nil
}

// dir walks a local directory and uploads all files under destPath,
// preserving the relative directory structure.
func (s *pushSession) dir(srcDir, nodeID, destPath string) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		osfDest := filepath.ToSlash(filepath.Join(destPath, rel))
		return s.file(path, nodeID, osfDest)
	})
}

// redundantOverwrite reports whether an "overwrite" upload would merely re-mint
// an identical remote version (local content already equals the remote content).
// Such an overwrite is skipped to keep push idempotent. It does not apply to
// "rename" (a deliberate duplicate) or "skip" (already handled).
func redundantOverwrite(action, localMD5, remoteMD5 string) bool {
	return action == "overwrite" && localMD5 != "" && localMD5 == remoteMD5
}

// uploadPlan describes how a single file upload should be carried out.
type uploadPlan struct {
	url    string // Waterbutler URL to PUT to; empty when action is "skip"
	name   string // final filename at the destination
	action string // "upload", "overwrite", "rename", or "skip"
}

// planUpload decides how to handle a file given the conflict mode and whether a
// file of the same name already exists at the destination. uploadBase is the
// ID-correct Waterbutler base for the destination folder (RootUploadURL or the
// folder's links.upload); new/rename URLs are derived from it. Pure and
// fully unit-testable.
func planUpload(
	conflict string,
	existing *client.FileItem,
	uploadBase, filename string,
	siblings []client.FileItem,
) (uploadPlan, error) {
	if existing == nil {
		return uploadPlan{
			url:    client.AppendUploadName(uploadBase, filename),
			name:   filename,
			action: "upload",
		}, nil
	}

	switch conflict {
	case "skip":
		return uploadPlan{name: filename, action: "skip"}, nil
	case "overwrite":
		return uploadPlan{url: existing.Links.Upload, name: filename, action: "overwrite"}, nil
	case "rename":
		name := findFreeName(filename, siblings)
		return uploadPlan{
			url:    client.AppendUploadName(uploadBase, name),
			name:   name,
			action: "rename",
		}, nil
	}
	return uploadPlan{}, fmt.Errorf("unknown conflict mode: %s", conflict)
}

// isRootDir reports whether an OSF parent path refers to the storage root.
func isRootDir(parentDir string) bool {
	return strings.Trim(parentDir, "/") == ""
}

// folderUploadBase returns the ID-correct Waterbutler upload base for parentDir
// in a node: the storage root URL for the root, otherwise the parent folder's
// own links.upload (resolved from the metadata API — osfstorage folders are
// addressed by opaque ID, not name).
func folderUploadBase(ctx context.Context, res *resolver.Resolver, nodeID, parentDir string) (string, error) {
	if isRootDir(parentDir) {
		return client.RootUploadURL(nodeID), nil
	}
	folder, err := res.Resolve(ctx, nodeID, parentDir)
	if err != nil {
		return "", fmt.Errorf("destination folder %s is not accessible (does it exist?): %w", parentDir, err)
	}
	if folder.Attributes.Kind != "folder" {
		return "", fmt.Errorf("destination %s is not a folder", parentDir)
	}
	if folder.Links.Upload == "" {
		return "", fmt.Errorf("destination folder %s has no upload link", parentDir)
	}
	return folder.Links.Upload, nil
}

// deriveUploadTarget splits an OSF destination path into the parent directory
// and the filename to upload as.
//
//   - A trailing slash means the destination is a directory: the source file
//     keeps its own name (e.g. "/data/" + "in.csv" → "/data", "in.csv").
//   - Otherwise the last path segment is the explicit target filename
//     (e.g. "/data/out.csv" → "/data", "out.csv").
//
// An empty destination is treated as the storage root.
func deriveUploadTarget(destPath, srcPath string) (parentDir, filename string) {
	if destPath == "" {
		destPath = "/"
	}
	isDir := strings.HasSuffix(destPath, "/")
	destPath = "/" + strings.Trim(destPath, "/")

	if isDir {
		// Destination is a directory; keep the source's basename.
		return cleanParent(destPath), filepath.Base(srcPath)
	}

	parent := filepath.Dir(destPath)
	name := filepath.Base(destPath)
	if name == "." || name == "/" || name == "" {
		// No explicit filename — fall back to the source basename.
		return cleanParent(destPath), filepath.Base(srcPath)
	}
	return cleanParent(parent), name
}

// cleanParent normalises a parent directory path to start with "/" and have no
// trailing slash (except the root itself, which stays "/").
func cleanParent(p string) string {
	return "/" + strings.Trim(p, "/")
}

// findFreeName appends _1, _2, … to base until no sibling has that name.
func findFreeName(filename string, siblings []client.FileItem) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	existing := make(map[string]bool, len(siblings))
	for _, s := range siblings {
		existing[s.Attributes.Name] = true
	}
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
		if !existing[candidate] {
			return candidate
		}
	}
}

func init() {
	pushCmd.Flags().BoolVar(&pushDryRun, "dry-run", false, "Show what would be uploaded without uploading")
	pushCmd.Flags().StringVar(&pushConflict, "conflict", "skip", "Conflict resolution: skip, overwrite, or rename")
	pushCmd.Flags().BoolVar(&pushNoTrack, "no-track", false, "Upload without recording in .gosf/gosf.toml")
	pushCmd.Flags().BoolVar(&pushNoCheckRemote, "no-check-remote", false, "Skip remote version lookups for bare push")
	pushCmd.Flags().IntVarP(&pushJobs, "jobs", "j", defaultScanJobs, "Number of files to scan against the remote concurrently")
	pushCmd.Flags().BoolVar(&pushForce, "force", false, "Bypass the confirmation prompt and authorize remote-newer rollbacks")
	pushCmd.Flags().BoolVar(&pushYes, "yes", false, "Bypass the confirmation prompt for safe pushes (new files, updates)")
	pushCmd.Flags().StringVar(&pushResolve, "resolve", "", "Resolve divergence by taking local: 'ours'")
	rootCmd.AddCommand(pushCmd)
}
