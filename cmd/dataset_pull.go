package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var (
	pullLatest    bool
	pullWorkspace bool
)

// datasetPullFile is one row of the dataset-pull JSON result.
type datasetPullFile struct {
	Local  string `json:"local"`
	Key    string `json:"key"`
	Action string `json:"action"` // downloaded | skipped (already matches)
}

// runDatasetPull downloads a dataset's published files from its archive
// remote. By default the pinned version's bytes are fetched (the manifest
// is the portability mechanism — plan §4.7); --latest fetches the newest
// published version and re-pins to it. Returns handled=false when slug
// names no dataset.
func runDatasetPull(ctx context.Context, slug string) (handled bool, err error) {
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

	// --workspace, or an unpublished dataset with a workspace remote,
	// pulls the mutable workspace bytes instead of archive bytes.
	if pullWorkspace || (ds.Record == "" && ds.ResolveWorkspace(m.Project.DefaultWorkspace) != "") {
		return true, runWorkspacePull(ctx, m, manifestPath, repoRoot, ds)
	}
	if ds.Record == "" {
		return true, fmt.Errorf("dataset %q has never been published — nothing to pull (datapin publish %s, or configure a workspace and datapin push %s)", slug, slug, slug)
	}
	bk, _, err := resolveArchive(ds, m)
	if err != nil {
		return true, err
	}

	target := backend.RecordID(ds.Record)
	repin := false
	if pullLatest {
		rec, err := bk.GetRecord(ctx, target)
		if err != nil {
			return true, err
		}
		for _, v := range rec.Versions {
			if v.IsLatest && string(v.ID) != ds.Record {
				target = v.ID
				repin = true
			}
		}
	}

	remoteFiles, err := bk.ListFiles(ctx, target)
	if err != nil {
		return true, err
	}
	remoteByKey := make(map[string]backend.FileInfo, len(remoteFiles))
	for _, f := range remoteFiles {
		remoteByKey[f.Key] = f
	}

	var results []datasetPullFile
	for i := range ds.Files {
		f := &ds.Files[i]
		rf, ok := remoteByKey[f.Key]
		if !ok {
			log.Warnf("key %s is tracked in the manifest but absent from record %s — skipping", f.Key, target)
			continue
		}
		dest := filepath.Join(repoRoot, f.Local)

		// The pin is the source of truth for a pinned pull: a listing that
		// contradicts it means the record does not carry the pinned bytes
		// at this key (the live Dataverse rename bug published v1's file
		// under v2's key). Fail loudly instead of delivering wrong bytes
		// and clobbering the pin.
		if !repin && f.MD5 != "" && rf.Checksum.Hex != "" && rf.Checksum.Hex != f.MD5 {
			return true, fmt.Errorf(
				"record %s file %s has checksum %s but the manifest pins %s — the published version does not match the pin (datapin pull %s --latest re-pins to what the remote serves)",
				target, f.Key, rf.Checksum.Hex, f.MD5, slug)
		}

		// Idempotent: a local file already matching the remote checksum is
		// not re-downloaded.
		if sum, err := computeLocalMD5(dest); err == nil && sum == rf.Checksum.Hex {
			results = append(results, datasetPullFile{Local: f.Local, Key: f.Key, Action: "skipped"})
			if repin {
				f.MD5 = rf.Checksum.Hex
			}
			continue
		}
		if pullDryRun {
			results = append(results, datasetPullFile{Local: f.Local, Key: f.Key, Action: "would download"})
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return true, err
		}
		if err := downloadDatasetFile(ctx, bk, target, rf.Key, dest); err != nil {
			return true, err
		}
		log.Infof("↓ %s → %s (%s)", f.Key, f.Local, output.FormatSize(rf.Size))
		f.MD5 = rf.Checksum.Hex
		results = append(results, datasetPullFile{Local: f.Local, Key: f.Key, Action: "downloaded"})
	}

	if repin && !pullDryRun {
		rec, err := bk.GetRecord(ctx, target)
		if err == nil {
			ds.Record = string(target)
			for _, v := range rec.Versions {
				if string(v.ID) == string(target) {
					ds.Version = v.Index + 1
					ds.VersionDOI = v.DOI
				}
			}
			ds.ConceptDOI = rec.ConceptDOI
			ds.Concept = rec.ConceptID
		}
	}
	if !pullDryRun {
		if err := manifest.Save(m, manifestPath); err != nil {
			return true, err
		}
	}

	if flagOutput == "json" {
		return true, output.PrintJSON(os.Stdout, map[string]any{
			"slug": slug, "record": string(target), "files": results, "dry_run": pullDryRun,
		})
	}
	return true, nil
}

// downloadDatasetFile streams one key to dest atomically (temp + rename);
// the driver verifies the stream checksum against the server's.
func downloadDatasetFile(ctx context.Context, bk backend.Backend, rec backend.RecordID, key, dest string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".datapin.pull.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := bk.DownloadFile(ctx, rec, key, tmp); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("downloading %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, dest)
}
