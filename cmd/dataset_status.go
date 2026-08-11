package cmd

import (
	"context"
	"fmt"

	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
)

// datasetStatus classifies one dataset for `datapin status`: local files
// vs their pins, and (unless noCheckRemote) the pinned version vs the
// archive's latest. Read-only.
func datasetStatus(ctx context.Context, m *manifest.Manifest, repoRoot string, ds *manifest.Dataset, noCheckRemote bool) (state, detail string, err error) {
	localMD5, _, missing, err := datasetLocalState(repoRoot, ds)
	if err != nil {
		return "", "", err
	}

	localChanged := false
	for _, f := range ds.Files {
		if sum, ok := localMD5[f.Local]; ok && sum != f.MD5 {
			localChanged = true
		}
	}

	published := ds.Record != ""
	remoteNewer := false
	latestNum := ds.Version
	if published && !noCheckRemote {
		bk, _, rerr := resolveArchive(ds, m)
		if rerr != nil {
			return "", "", rerr
		}
		_, _, latest, rerr := remoteDatasetState(ctx, bk, ds)
		if rerr != nil {
			return "", "", rerr
		}
		latestNum = latest
		remoteNewer = latest > ds.Version
	}

	state = datasetState(published, localChanged, remoteNewer, len(missing) > 0)
	switch state {
	case "NOT_PUBLISHED":
		detail = fmt.Sprintf("dataset (%d files) — never published", len(ds.Files))
	case "MISSING":
		detail = fmt.Sprintf("dataset — %d local file(s) missing (datapin pull %s)", len(missing), ds.Slug)
	case "AHEAD":
		detail = fmt.Sprintf("dataset — local changes since v%d (datapin publish %s)", ds.Version, ds.Slug)
	case "REMOTE_NEWER":
		detail = fmt.Sprintf("dataset — archive has v%d (datapin pull %s --latest)", latestNum, ds.Slug)
	case "DIVERGED":
		detail = fmt.Sprintf("dataset — local changed AND archive has v%d", latestNum)
	default:
		detail = fmt.Sprintf("dataset — published as v%d", ds.Version)
	}
	return state, detail, nil
}

// datasetStateGlyph mirrors stateDisplay's glyph conventions.
func datasetStateGlyph(state string) string {
	switch state {
	case "IN_SYNC":
		return "✓"
	case "MISSING":
		return "✗"
	case "REMOTE_NEWER":
		return "↑"
	case "NOT_PUBLISHED":
		return "·"
	default:
		return state
	}
}

// datasetStateStyle mirrors stateStyle's colors for dataset states.
func datasetStateStyle(state string) func(string) string {
	switch state {
	case "IN_SYNC":
		return output.Green
	case "MISSING":
		return output.Red
	case "DIVERGED":
		return output.RedBold
	case "AHEAD":
		return output.Yellow
	case "REMOTE_NEWER":
		return output.Cyan
	case "NOT_PUBLISHED":
		return output.Dim
	default:
		return nil
	}
}
