package cmd

import (
	"context"
	"fmt"

	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
)

// datasetStatusResult is one dataset row: where it stands on the archive
// track and, independently, on the workspace track. The two are separate
// remotes with separate roles (D33), so they get separate states — the
// archive state answers "is the published record current?" and the
// workspace state answers "am I current with my collaborators?".
type datasetStatusResult struct {
	State  string
	Detail string
	// RemoteLatest is the archive's latest version number (0 when
	// unpublished or unchecked).
	RemoteLatest int
	// WorkspaceRemote is the resolved workspace remote name ("" when the
	// dataset has none, or the check was skipped) and WorkspaceState the
	// aggregate over WorkspaceKeys.
	WorkspaceRemote string
	WorkspaceState  string
	WorkspaceKeys   []workspaceKeyStatus
}

// datasetStatus classifies one dataset for `datapin status`: local files
// vs their pins, the pinned version vs the archive's latest, and local
// content vs the workspace remote's journal head (all remote reads
// skipped under noCheckRemote). Read-only on both tracks.
func datasetStatus(ctx context.Context, m *manifest.Manifest, repoRoot string, ds *manifest.Dataset, noCheckRemote bool) (datasetStatusResult, error) {
	var res datasetStatusResult
	localMD5, _, missing, err := datasetLocalState(repoRoot, ds)
	if err != nil {
		return res, err
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
			return res, rerr
		}
		_, _, latest, rerr := remoteDatasetState(ctx, bk, ds)
		if rerr != nil {
			return res, rerr
		}
		latestNum = latest
		remoteNewer = latest > ds.Version
	}

	state := datasetState(published, localChanged, remoteNewer, len(missing) > 0)
	var detail string
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

	res.State = state
	res.Detail = detail
	res.RemoteLatest = latestNum
	if wsDetail := datasetWorkspaceRow(ctx, m, ds, localMD5, noCheckRemote, &res); wsDetail != "" {
		res.Detail += " · " + wsDetail
	}
	return res, nil
}

// datasetWorkspaceRow fills in the workspace half of a dataset row and
// returns the human clause for its DETAIL ("" when there is nothing to
// say). A dataset with no workspace remote does no I/O at all — the
// common case today — and a workspace that cannot be reached is a warning
// rather than a failed run: status must still report the archive answer,
// which is the primary track.
func datasetWorkspaceRow(ctx context.Context, m *manifest.Manifest, ds *manifest.Dataset, localMD5 map[string]string, noCheckRemote bool, res *datasetStatusResult) string {
	if noCheckRemote {
		return ""
	}
	name := ds.ResolveWorkspace(m.Project.DefaultWorkspace)
	if name == "" {
		return ""
	}
	res.WorkspaceRemote = name

	ws, _, closer, err := resolveWorkspace(ds, m)
	if err == nil {
		defer func() { _ = closer() }()
		res.WorkspaceKeys, err = datasetWorkspaceStatus(ctx, ws, ds, localMD5)
	}
	if err != nil {
		log.Warnf("dataset %q: workspace %q could not be read — reporting the archive state only: %v", ds.Slug, name, err)
		res.WorkspaceState = wsStateUnknown
		return workspaceDetail(wsStateUnknown, nil, name, ds.Slug, err)
	}
	res.WorkspaceState = aggregateWorkspaceState(res.WorkspaceKeys)
	return workspaceDetail(res.WorkspaceState, res.WorkspaceKeys, name, ds.Slug, nil)
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
