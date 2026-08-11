package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

// scanEntries classifies every file entry in the manifest against the remote,
// concurrently, and returns the per-entry plans in manifest order. The returned
// plans hold pointers into m.Files, so executing a plan mutates the manifest.
//
// Classification is the dominant cost of sync/push/status (OSF's metadata API
// is ~1–3s per request), so the entries are processed through a bounded worker
// pool over a caching resolver: files sharing a directory collapse to one
// listing, and version history is skipped whenever the listing already settles
// the state (see fetchRemoteState).
func scanEntries(
	ctx context.Context,
	m *manifest.Manifest,
	repoRoot string,
	res *resolver.Resolver,
	osfClient *client.OSFClient,
	jobs int,
	noCheckRemote bool,
) ([]entryPlan, error) {
	total := len(m.Files)
	plans := make([]entryPlan, total)
	var scanned int64

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(scanConcurrency(jobs))
	for i := range m.Files {
		i := i
		g.Go(func() error {
			entry := &m.Files[i]
			proj := entry.ResolveProject(m.Project.ID)
			localAbs := filepath.Join(repoRoot, entry.Local)

			localMD5, err := computeLocalMD5(localAbs)
			if err != nil {
				return fmt.Errorf("computing MD5 for %s: %w", entry.Local, err)
			}

			var remoteVersions []manifest.RemoteVersion
			var resolvedItem *client.FileItem
			if !noCheckRemote {
				resolvedItem, remoteVersions, err = fetchRemoteState(gctx, res, osfClient, proj, *entry, localMD5, true)
				if err != nil {
					return err
				}
				log.Infof("scanned remote %d/%d  %s", atomic.AddInt64(&scanned, 1), total, entry.Local)
			} else {
				log.Debugf("classifying %d/%d  %s (no remote check)", atomic.AddInt64(&scanned, 1), total, entry.Local)
			}

			plans[i] = entryPlan{
				entry: entry, proj: proj, localAbs: localAbs, localMD5: localMD5,
				state:        manifest.ClassifyFile(*entry, localMD5, remoteVersions, noCheckRemote),
				resolvedItem: resolvedItem, remoteVersions: remoteVersions,
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return plans, nil
}

// scanWikiEntries is the wiki analogue of scanEntries. It runs serially: wiki
// entries are usually a handful, and the per-project listing cache already
// collapses the repeated fetches.
func scanWikiEntries(
	ctx context.Context,
	m *manifest.Manifest,
	repoRoot string,
	osfClient *client.OSFClient,
	noCheckRemote bool,
) ([]wikiEntryPlan, error) {
	wcache := newWikiScanCache(osfClient)
	plans := make([]wikiEntryPlan, len(m.Wikis))
	for i := range m.Wikis {
		we := &m.Wikis[i]
		proj := we.ResolveProject(m.Project.ID)
		localAbs := filepath.Join(repoRoot, we.Local)
		localMD5, err := wikiLocalMD5(localAbs)
		if err != nil {
			return nil, fmt.Errorf("computing MD5 for %s: %w", we.Local, err)
		}

		var page *client.Wiki
		var remoteVersions []manifest.RemoteVersion
		if !noCheckRemote {
			page, remoteVersions, err = fetchWikiRemoteState(ctx, wcache, osfClient, proj, *we, localMD5)
			if err != nil {
				return nil, err
			}
			log.Infof("scanned wiki %s", we.Local)
		}

		plans[i] = wikiEntryPlan{
			entry: we, proj: proj, localAbs: localAbs, localMD5: localMD5,
			state: manifest.ClassifyFile(we.BaselineEntry(), localMD5, remoteVersions, noCheckRemote),
			page:  page, remoteVersions: remoteVersions,
		}
	}
	return plans, nil
}
