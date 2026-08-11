package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/workspace"
)

// --- datapin push <slug> (dataset → workspace) ---

// workspacePushFile is one row of the JSON result.
type workspacePushFile struct {
	Local  string `json:"local"`
	Key    string `json:"key"`
	Action string `json:"action"` // pushed | unchanged
	Seq    int    `json:"seq,omitempty"`
	MD5    string `json:"md5"`
}

// runDatasetPush pushes a dataset's current files to its workspace remote
// with the journal scheme: superseded versions archive server-side, the
// event log narrates what happened, and every version datapin writes is
// revertible. Returns handled=false when slug names no dataset.
func runDatasetPush(ctx context.Context, slug string) (handled bool, err error) {
	manifestPath, repoRoot, err := manifest.FindManifest()
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
	if len(ds.Files) == 0 {
		return true, fmt.Errorf("dataset %q has no files to push", slug)
	}

	localMD5, sizes, missing, err := datasetLocalState(repoRoot, ds)
	if err != nil {
		return true, err
	}
	if len(missing) > 0 {
		return true, fmt.Errorf("cannot push with missing local files: %v", missing)
	}

	ws, remote, closer, err := resolveWorkspace(ds, m)
	if err != nil {
		return true, err
	}
	defer func() { _ = closer() }()

	by := pushAuthor()
	var results []workspacePushFile
	for _, f := range ds.Files {
		sum := localMD5[f.Local]
		if pushDryRun {
			results = append(results, workspacePushFile{Local: f.Local, Key: f.Key, Action: "would push", MD5: sum})
			continue
		}
		src, err := os.Open(filepath.Join(repoRoot, f.Local))
		if err != nil {
			return true, err
		}
		ev, err := ws.Push(ctx, f.Key, src, sizes[f.Local], sum, by)
		_ = src.Close()
		if err != nil {
			return true, fmt.Errorf("pushing %s: %w", f.Key, err)
		}
		if ev.NoOp {
			results = append(results, workspacePushFile{Local: f.Local, Key: f.Key, Action: "unchanged", Seq: ev.Seq, MD5: sum})
			continue
		}
		if ev.OutOfBandOverwrite {
			log.Warnf("%s: the workspace copy was changed outside datapin — its bytes were archived before overwriting", f.Key)
		}
		log.Infof("↑ %s → %s@%s (v%d, %s)", f.Local, remote.Name, f.Key, ev.Seq, output.FormatSize(sizes[f.Local]))
		results = append(results, workspacePushFile{Local: f.Local, Key: f.Key, Action: "pushed", Seq: ev.Seq, MD5: sum})
	}

	if flagOutput == "json" {
		return true, output.PrintJSON(os.Stdout, map[string]any{
			"slug": slug, "remote": remote.Name, "files": results, "dry_run": pushDryRun,
		})
	}
	return true, nil
}

// pushAuthor identifies the pusher in journal events (best effort).
func pushAuthor() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "datapin"
}

// --- datapin revert <slug>/<key> --to N ---

var (
	revertTo     int
	revertReason string
)

var revertCmd = &cobra.Command{
	Use:   "revert <slug>/<key> --to <n>",
	Short: "Restore an earlier workspace version of a file (journaled)",
	Long: `Restore version <n> of a dataset file on its workspace remote. The
restore is a NEW journal event — history never rewrites, and the
regretted version stays retrievable. Run 'datapin pull <slug> --workspace'
afterwards to update the local copy.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if revertTo <= 0 {
			return fmt.Errorf("--to <n> is required (see: datapin versions %s)", args[0])
		}
		manifestPath, _, err := manifest.FindManifest()
		if err != nil {
			return err
		}
		m, err := manifest.Load(manifestPath)
		if err != nil {
			return err
		}
		ds, key := splitSlugKey(m, args[0])
		if ds == nil || key == "" {
			return fmt.Errorf("argument must be <slug>/<key> naming a dataset file (got %q)", args[0])
		}
		ws, remote, closer, err := resolveWorkspace(ds, m)
		if err != nil {
			return err
		}
		defer func() { _ = closer() }()

		ev, err := ws.Revert(cmd.Context(), key, revertTo, pushAuthor(), revertReason)
		if err != nil {
			return err
		}
		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, map[string]any{
				"slug": ds.Slug, "key": key, "remote": remote.Name,
				"reverted_to": revertTo, "new_seq": ev.Seq, "md5": ev.MD5,
			})
		}
		log.Infof("✓ %s@%s reverted to v%d (journaled as v%d) — 'datapin pull %s --workspace' to update local files",
			remote.Name, key, revertTo, ev.Seq, ds.Slug)
		return nil
	},
}

// --- datapin gc --keep N ---

var gcKeep int

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Reclaim archived workspace versions",
	Long: `Reclaim archived version blobs on every workspace remote the manifest's
datasets use, keeping the --keep most recent archived versions per file
(the current version never counts against the budget). The journal is
never rewritten: reclaimed versions remain listed and report themselves
unrecoverable.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		manifestPath, _, err := manifest.FindManifest()
		if err != nil {
			return err
		}
		m, err := manifest.Load(manifestPath)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		type gcResult struct {
			Remote string `json:"remote"`
			Freed  int64  `json:"freed_bytes"`
		}
		var results []gcResult
		for i := range m.Datasets {
			ds := &m.Datasets[i]
			name := ds.ResolveWorkspace(m.Project.DefaultWorkspace)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			ws, remote, closer, err := resolveWorkspace(ds, m)
			if err != nil {
				return err
			}
			freed, err := ws.GC(cmd.Context(), gcKeep)
			_ = closer()
			if err != nil {
				return err
			}
			log.Infof("gc on %q: reclaimed %s (keeping %d archived version(s) per file)", remote.Name, output.FormatSize(freed), gcKeep)
			results = append(results, gcResult{Remote: remote.Name, Freed: freed})
		}
		if len(results) == 0 {
			return fmt.Errorf("no datasets reference a workspace remote — nothing to gc")
		}
		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, results)
		}
		return nil
	},
}

// --- workspace side of versions and pull ---

// runWorkspaceVersions lists a key's journal. Called from the versions
// command when the argument is <slug>/<key>.
func runWorkspaceVersions(ctx context.Context, m *manifest.Manifest, ds *manifest.Dataset, key string) error {
	ws, remote, closer, err := resolveWorkspace(ds, m)
	if err != nil {
		return err
	}
	defer func() { _ = closer() }()

	events, err := ws.Versions(ctx, key)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return fmt.Errorf("%s has no workspace history on %q — push it first (datapin push %s)", key, remote.Name, ds.Slug)
	}

	if flagOutput == "json" {
		type row struct {
			Seq         int    `json:"seq"`
			Action      string `json:"action"`
			MD5         string `json:"md5"`
			Size        int64  `json:"size"`
			Time        string `json:"time"`
			By          string `json:"by,omitempty"`
			To          int    `json:"to,omitempty"`
			Reason      string `json:"reason,omitempty"`
			OutOfBand   bool   `json:"out_of_band_overwrite,omitempty"`
			Recoverable bool   `json:"recoverable"`
		}
		rows := make([]row, 0, len(events))
		for _, e := range events {
			rows = append(rows, row{e.Seq, e.Action, e.MD5, e.Size, e.Time, e.By, e.To, e.Reason, e.OutOfBandOverwrite, e.Recoverable})
		}
		return output.PrintJSON(os.Stdout, map[string]any{
			"slug": ds.Slug, "key": key, "remote": remote.Name, "events": rows,
		})
	}

	rows := make([][]output.Cell, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- { // newest first
		e := events[i]
		what := e.Action
		if e.Action == "revert" {
			what = fmt.Sprintf("revert→v%d", e.To)
			if e.Reason != "" {
				what += " (" + e.Reason + ")"
			}
		}
		if e.OutOfBandOverwrite {
			what += " ⚠ out-of-band overwrite archived"
		}
		state := ""
		style := output.Green
		if !e.Recoverable {
			state = "unrecoverable"
			style = output.Red
		}
		rows = append(rows, []output.Cell{
			{Text: fmt.Sprintf("v%d", e.Seq), Style: output.Bold},
			{Text: e.Time},
			{Text: e.By},
			{Text: output.FormatSize(e.Size)},
			{Text: what},
			{Text: state, Style: style},
		})
	}
	output.RenderTable(os.Stdout, []string{"VER", "TIME", "BY", "SIZE", "EVENT", ""}, rows)
	return nil
}

// runWorkspacePull downloads the current workspace bytes of every
// dataset file (idempotent by checksum).
func runWorkspacePull(ctx context.Context, m *manifest.Manifest, manifestPath, repoRoot string, ds *manifest.Dataset) error {
	ws, remote, closer, err := resolveWorkspace(ds, m)
	if err != nil {
		return err
	}
	defer func() { _ = closer() }()

	var results []datasetPullFile
	for _, f := range ds.Files {
		sum, exists, err := ws.HeadMD5(ctx, f.Key)
		if err != nil {
			return err
		}
		if !exists {
			log.Warnf("%s is not on workspace %q — skipping", f.Key, remote.Name)
			continue
		}
		dest := filepath.Join(repoRoot, f.Local)
		if local, err := computeLocalMD5(dest); err == nil && local == sum {
			results = append(results, datasetPullFile{Local: f.Local, Key: f.Key, Action: "skipped"})
			continue
		}
		if pullDryRun {
			results = append(results, datasetPullFile{Local: f.Local, Key: f.Key, Action: "would download"})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := atomicWorkspaceDownload(ctx, ws, f.Key, dest); err != nil {
			return err
		}
		log.Infof("↓ %s@%s → %s", remote.Name, f.Key, f.Local)
		results = append(results, datasetPullFile{Local: f.Local, Key: f.Key, Action: "downloaded"})
	}

	if flagOutput == "json" {
		return output.PrintJSON(os.Stdout, map[string]any{
			"slug": ds.Slug, "remote": remote.Name, "workspace": true,
			"files": results, "dry_run": pullDryRun,
		})
	}
	return nil
}

func atomicWorkspaceDownload(ctx context.Context, ws *workspace.Workspace, key, dest string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".datapin.pull.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := ws.Get(ctx, key, tmp); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("downloading %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, dest)
}

func init() {
	revertCmd.Flags().IntVar(&revertTo, "to", 0, "Version (journal seq) to restore")
	revertCmd.Flags().StringVar(&revertReason, "reason", "", "Why this version is being restored (recorded in the journal)")
	gcCmd.Flags().IntVar(&gcKeep, "keep", 3, "Archived versions to keep per file")
	rootCmd.AddCommand(revertCmd, gcCmd)
}
