package cmd

import (
	"context"
	"fmt"

	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/workspace"
)

// The dataset-level workspace states reported by `datapin status`. They
// aggregate the per-key workspace.KeyState values over the dataset's whole
// file set, the same way datasetState aggregates the archive comparison.
const (
	// wsStateNone: no workspace remote resolves for the dataset (no
	// `workspace` field, no `default_workspace`), or the check was
	// suppressed. Nothing is probed and nothing is reported.
	wsStateNone = ""
	// wsStateInSync: every key that exists matches the workspace head.
	wsStateInSync = "IN_SYNC"
	// wsStateNotPushed: at least one local file is not on the workspace,
	// and nothing needs pulling.
	wsStateNotPushed = "NOT_PUSHED"
	// wsStateMissing: at least one key is on the workspace but absent
	// locally, and nothing needs pushing.
	wsStateMissing = "MISSING"
	// wsStateBehind: the workspace has newer bytes for at least one key
	// (local content matches an older journaled version), and nothing
	// needs pushing.
	wsStateBehind = "BEHIND"
	// wsStateAhead: at least one key holds local content the workspace has
	// never seen, and nothing needs pulling.
	wsStateAhead = "AHEAD"
	// wsStateDiverged: some keys need pushing AND others need pulling, so
	// no single command reconciles the dataset.
	wsStateDiverged = "DIVERGED"
	// wsStateUnknown: the workspace could not be reached or read. status
	// still reports the archive answer (the primary track) rather than
	// failing the run, but it never claims sync it could not verify.
	wsStateUnknown = "UNKNOWN"
)

// workspaceKeyStatus is one dataset file's standing against the workspace.
type workspaceKeyStatus struct {
	Local string
	Key   string
	State workspace.KeyState
	// LocalMD5 is "" when the local file is absent.
	LocalMD5 string
	// HeadMD5 is "" when the workspace has no object at the key.
	HeadMD5 string
	// HeadSeq is the journal head's sequence number (0 = no journal).
	HeadSeq int
}

// workspaceProber is the slice of *workspace.Workspace that status needs:
// the current content address of a key, and the key's journal. Both are
// read-only — status never mutates a remote.
type workspaceProber interface {
	HeadMD5(ctx context.Context, key string) (string, bool, error)
	Journal(ctx context.Context, key string) ([]workspace.Event, error)
}

// datasetWorkspaceStatus classifies every tracked key against the
// workspace remote. localMD5 is keyed by the manifest's local path, as
// datasetLocalState returns it; a key absent from the map has no local
// file.
//
// Cost: one head read per key. The journal is read only for keys that
// actually drifted, because that is the only case where history can
// change the answer (an older journaled version turns AHEAD into BEHIND).
func datasetWorkspaceStatus(ctx context.Context, p workspaceProber, ds *manifest.Dataset, localMD5 map[string]string) ([]workspaceKeyStatus, error) {
	out := make([]workspaceKeyStatus, 0, len(ds.Files))
	for _, f := range ds.Files {
		ks := workspaceKeyStatus{Local: f.Local, Key: f.Key, LocalMD5: localMD5[f.Local]}
		head, exists, err := p.HeadMD5(ctx, f.Key)
		if err != nil {
			return nil, fmt.Errorf("reading %s from the workspace: %w", f.Key, err)
		}
		if exists {
			ks.HeadMD5 = head
		}
		var journal []workspace.Event
		if ks.HeadMD5 != "" && ks.LocalMD5 != "" && ks.HeadMD5 != ks.LocalMD5 {
			journal, err = p.Journal(ctx, f.Key)
			if err != nil {
				return nil, fmt.Errorf("reading the journal for %s: %w", f.Key, err)
			}
		}
		if n := len(journal); n > 0 {
			ks.HeadSeq = journal[n-1].Seq
		}
		ks.State = workspace.ClassifyKey(ks.LocalMD5, ks.HeadMD5, journal)
		out = append(out, ks)
	}
	return out, nil
}

// aggregateWorkspaceState reduces per-key states to the one state the
// dataset's row reports. A dataset is one publishable record and gets one
// row, so the precedence is: divergence (work in both directions) first,
// then the push side, then the pull side. Keys that exist on neither side
// carry no workspace work and are excluded — a tracked file that is
// nowhere is already the archive column's MISSING.
//
// The full per-key breakdown is preserved in `--output=json`, so nothing
// this collapses is lost to a script.
func aggregateWorkspaceState(keys []workspaceKeyStatus) string {
	var ahead, behind, missing, unpushed, inSync int
	for _, k := range keys {
		switch k.State {
		case workspace.KeyAhead:
			ahead++
		case workspace.KeyBehind:
			behind++
		case workspace.KeyMissing:
			missing++
		case workspace.KeyNotPushed:
			if k.LocalMD5 != "" {
				unpushed++
			}
		case workspace.KeyInSync:
			inSync++
		}
	}
	needsPush := ahead > 0 || unpushed > 0
	needsPull := behind > 0 || missing > 0
	switch {
	case needsPush && needsPull:
		return wsStateDiverged
	case ahead > 0:
		return wsStateAhead
	case unpushed > 0:
		return wsStateNotPushed
	case behind > 0:
		return wsStateBehind
	case missing > 0:
		return wsStateMissing
	case inSync > 0:
		return wsStateInSync
	default:
		return wsStateNotPushed
	}
}

// workspaceStateIsInSync reports whether a workspace state should count as
// "no work to do" for the CI-friendly exit code.
//
// Real drift counts. The mere *absence* of a workspace copy does not: the
// workspace track is optional (unlike publishing, which is the point of a
// dataset), so a published dataset that simply never pushed to its
// workspace must not start failing a pipeline that passes today.
// wsStateUnknown is not in sync — a workspace datapin could not read is a
// state it cannot vouch for.
func workspaceStateIsInSync(state string) bool {
	switch state {
	case wsStateNone, wsStateInSync, wsStateNotPushed:
		return true
	default:
		return false
	}
}

// workspaceDetail renders the human clause appended to a dataset row's
// DETAIL, naming the remedy command for the state.
func workspaceDetail(state string, keys []workspaceKeyStatus, remoteName, slug string, probeErr error) string {
	if state == wsStateNone {
		return ""
	}
	prefix := fmt.Sprintf("workspace %q: ", remoteName)
	if state == wsStateUnknown {
		return prefix + fmt.Sprintf("could not be read — %v", probeErr)
	}
	count := func(want workspace.KeyState) int {
		n := 0
		for _, k := range keys {
			if k.State == want {
				n++
			}
		}
		return n
	}
	switch state {
	case wsStateInSync:
		return prefix + "in sync"
	case wsStateNotPushed:
		return prefix + fmt.Sprintf("%d file(s) not on the workspace (datapin push %s)", count(workspace.KeyNotPushed), slug)
	case wsStateMissing:
		return prefix + fmt.Sprintf("%d file(s) missing locally (datapin pull %s --workspace)", count(workspace.KeyMissing), slug)
	case wsStateBehind:
		return prefix + fmt.Sprintf("%d file(s) behind — newer bytes were pushed (datapin pull %s --workspace)",
			count(workspace.KeyBehind), slug)
	case wsStateAhead:
		// There is no workspace baseline pin, so "local moved" and "local
		// moved AND someone else pushed" are indistinguishable per key.
		// Name both remedies instead of guessing which one is meant.
		return prefix + fmt.Sprintf("%d file(s) hold local content the workspace has not seen — datapin push %s to share, datapin pull %s --workspace to discard",
			count(workspace.KeyAhead), slug, slug)
	case wsStateDiverged:
		push := count(workspace.KeyAhead) + count(workspace.KeyNotPushed)
		pull := count(workspace.KeyBehind) + count(workspace.KeyMissing)
		return prefix + fmt.Sprintf("%d file(s) to push and %d to pull — reconcile per file (datapin versions %s/<key>)", push, pull, slug)
	default:
		return prefix + state
	}
}

// workspaceStateGlyph mirrors stateDisplay's glyph conventions for the
// WORKSPACE column.
func workspaceStateGlyph(state string) string {
	switch state {
	case wsStateNone:
		return "—"
	case wsStateInSync:
		return "✓"
	case wsStateMissing:
		return "✗"
	case wsStateNotPushed:
		return "·"
	case wsStateUnknown:
		return "?"
	default:
		return state
	}
}

// workspaceStateStyle mirrors stateStyle's colors for workspace states.
func workspaceStateStyle(state string) func(string) string {
	switch state {
	case wsStateInSync:
		return output.Green
	case wsStateMissing:
		return output.Red
	case wsStateDiverged:
		return output.RedBold
	case wsStateAhead:
		return output.Yellow
	case wsStateBehind:
		return output.Cyan
	case wsStateNotPushed, wsStateNone:
		return output.Dim
	case wsStateUnknown:
		return output.Yellow
	default:
		return nil
	}
}

// workspaceStatusFiles renders the per-key breakdown for
// `datapin status --output=json`, so a script never has to accept the
// row's aggregation. Nil for a dataset with no workspace, which keeps the
// field omitted entirely.
func workspaceStatusFiles(keys []workspaceKeyStatus) []output.StatusWorkspaceFile {
	if len(keys) == 0 {
		return nil
	}
	out := make([]output.StatusWorkspaceFile, 0, len(keys))
	for _, k := range keys {
		out = append(out, output.StatusWorkspaceFile{
			Local: k.Local, Key: k.Key, State: string(k.State), Version: k.HeadSeq,
		})
	}
	return out
}

// statusShowsWorkspaceColumn reports whether `datapin status` should render
// a WORKSPACE column. It only appears when it can carry information, so a
// legacy OSF-only manifest — or a `--no-check-remote` run, which does no
// remote I/O at all — sees exactly the table it sees today.
func statusShowsWorkspaceColumn(m *manifest.Manifest, noCheckRemote bool) bool {
	if noCheckRemote {
		return false
	}
	for i := range m.Datasets {
		if m.Datasets[i].ResolveWorkspace(m.Project.DefaultWorkspace) != "" {
			return true
		}
	}
	return false
}
