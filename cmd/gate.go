package cmd

import (
	"fmt"
	"strings"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// entryPlan is the classified, not-yet-executed reconciliation of one manifest
// entry: enough state to pre-flight (e.g. detect divergence across all entries)
// before any transfer runs.
type entryPlan struct {
	entry          *manifest.Entry
	proj           string
	localAbs       string
	localMD5       string
	state          manifest.FileState
	resolvedItem   *client.FileItem
	remoteVersions []manifest.RemoteVersion
}

// syncAction is the single reconciliation step chosen for one entry.
//
// Which one applies is decided entirely by the classified state — how local
// content, the pinned baseline, and the remote compare *now* — plus the flags
// given on this run. Nothing recorded in the manifest steers it: every state
// has one correct action, and the two states that genuinely do not
// (AHEAD_OF_MANIFEST, DIVERGED) are reported for the user to resolve rather
// than guessed at (issue #81).
type syncAction int

const (
	actionNone    syncAction = iota // nothing to do
	actionPin                       // record version + md5; no bytes move
	actionPull                      // download the remote's latest, re-pin
	actionPush                      // upload the local file, re-pin
	actionRestore                   // overwrite locally-modified content from the remote
	actionReport                    // ambiguous — report it, transfer nothing
	actionBlocked                   // diverged with no --resolve: fail the run
)

func (a syncAction) String() string {
	switch a {
	case actionNone:
		return "none"
	case actionPin:
		return "pin"
	case actionPull:
		return "pull"
	case actionPush:
		return "push"
	case actionRestore:
		return "restore"
	case actionReport:
		return "report"
	case actionBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// syncDecision is the whole of `gosf sync`'s policy: reconcile everything that
// has an unambiguous answer, report what does not.
//
//	IN_SYNC             nothing
//	PIN_ONLY            record version + md5, no transfer
//	MISSING             download (writing a file that does not exist destroys
//	                    nothing — it is the safest transfer gosf can perform,
//	                    and needs no --force)
//	BEHIND              fast-forward
//	REMOTE_NEWER        fast-forward
//	NOT_PUSHED + local  upload (registering the file was the intent)
//	NOT_PUSHED, no local skip — there is nothing anywhere
//	AHEAD_OF_MANIFEST   report; --force discards the local change instead
//	DIVERGED            blocked; --resolve picks a side, honored as given
func syncDecision(state manifest.FileState, localExists, force bool, resolve string) syncAction {
	switch state {
	case manifest.StateInSync:
		return actionNone
	case manifest.StatePinOnly:
		return actionPin
	case manifest.StateMissing, manifest.StateBehind, manifest.StateRemoteNewer:
		return actionPull
	case manifest.StateNotPushed:
		if localExists {
			return actionPush
		}
		return actionNone
	case manifest.StateAheadOfManifest:
		if force {
			return actionRestore
		}
		return actionReport
	case manifest.StateDivergent:
		return resolveDecision(resolve)
	}
	return actionNone
}

// pushDecision is bare `gosf push`: publish local work. It selects the states
// where local holds content the remote does not, plus a deliberate rollback
// under --force. Where local content is already on the remote (REMOTE_NEWER,
// BEHIND) a push would only bury a newer version while adding nothing, so it is
// skipped rather than refused — one such entry must not block the whole run.
func pushDecision(state manifest.FileState, localExists, force bool, resolve string) syncAction {
	switch state {
	case manifest.StateAheadOfManifest:
		return actionPush
	case manifest.StateNotPushed:
		if localExists {
			return actionPush
		}
		return actionNone
	case manifest.StatePinOnly:
		return actionPin
	case manifest.StateRemoteNewer, manifest.StateBehind:
		if force {
			return actionPush
		}
		return actionNone
	case manifest.StateDivergent:
		if resolve == "ours" {
			return actionPush
		}
		return actionBlocked
	}
	return actionNone
}

// pullDecision is bare `gosf pull`: fetch what the remote has and local does
// not. It never uploads, so --resolve=ours is not a resolution it can apply.
func pullDecision(state manifest.FileState, force bool, resolve string) syncAction {
	switch state {
	case manifest.StateMissing, manifest.StateBehind, manifest.StateRemoteNewer:
		return actionPull
	case manifest.StatePinOnly:
		return actionPin
	case manifest.StateAheadOfManifest:
		if force {
			return actionRestore
		}
		return actionReport
	case manifest.StateDivergent:
		if resolve == "theirs" {
			return actionPull
		}
		return actionBlocked
	}
	return actionNone
}

// resolveDecision maps an explicit --resolve to the side it takes. An
// at-the-moment choice is strictly more current than anything on the entry, so
// it is honored unconditionally.
func resolveDecision(resolve string) syncAction {
	switch resolve {
	case "ours":
		return actionPush
	case "theirs":
		return actionPull
	default:
		return actionBlocked
	}
}

// validateResolve checks a --resolve flag value.
func validateResolve(resolve string) error {
	switch resolve {
	case "", "ours", "theirs":
		return nil
	default:
		return fmt.Errorf("--resolve must be 'ours' or 'theirs', got %q", resolve)
	}
}

// latestRemoteVersionInfo returns the remote version with the highest version
// number, or a zero RemoteVersion when the list is empty.
func latestRemoteVersionInfo(remoteVersions []manifest.RemoteVersion) manifest.RemoteVersion {
	var latest manifest.RemoteVersion
	for _, rv := range remoteVersions {
		if rv.Version >= latest.Version {
			latest = rv
		}
	}
	return latest
}

// pushActionLabel returns the short verb describing what pushing an entry in the
// given state will do, for the confirmation display.
func pushActionLabel(state manifest.FileState) string {
	switch state {
	case manifest.StateNotPushed:
		return "new"
	case manifest.StateAheadOfManifest:
		return "update"
	case manifest.StatePinOnly:
		return "pin"
	case manifest.StateInSync:
		return "unchanged"
	case manifest.StateRemoteNewer, manifest.StateBehind:
		return "rollback"
	case manifest.StateDivergent:
		return "diverged"
	case manifest.StateMissing:
		return "missing"
	default:
		return "skip"
	}
}

// needsPushConfirmation reports whether any state will write bytes to the remote
// (a new file or a new version), warranting an interactive confirmation. Pins,
// unchanged, and skipped entries do not write remote data.
func needsPushConfirmation(states []manifest.FileState) bool {
	for _, s := range states {
		if s == manifest.StateNotPushed || s == manifest.StateAheadOfManifest ||
			s == manifest.StateRemoteNewer || s == manifest.StateBehind || s == manifest.StateDivergent {
			return true
		}
	}
	return false
}

// summarizePush renders a one-line tally of a push plan, e.g.
// "3 new, 2 updated, 3 unchanged (skipped)".
func summarizePush(states []manifest.FileState) string {
	var newN, updated, unchanged, rollback, missing int
	for _, s := range states {
		switch s {
		case manifest.StateNotPushed:
			newN++
		case manifest.StateAheadOfManifest:
			updated++
		case manifest.StatePinOnly, manifest.StateInSync:
			unchanged++
		case manifest.StateRemoteNewer, manifest.StateBehind:
			rollback++
		case manifest.StateMissing:
			missing++
		}
	}
	parts := []string{
		fmt.Sprintf("%d new", newN),
		fmt.Sprintf("%d updated", updated),
		fmt.Sprintf("%d unchanged (skipped)", unchanged),
	}
	if rollback > 0 {
		parts = append(parts, fmt.Sprintf("%d rollback", rollback))
	}
	if missing > 0 {
		parts = append(parts, fmt.Sprintf("%d missing", missing))
	}
	return strings.Join(parts, ", ")
}

// divergenceError builds the hard-failure diagnostic for an entry where both the
// local file and the remote have changed since the pinned baseline. It names the
// three states and gives copy-pasteable resolution commands, per epic #38.
func divergenceError(entry manifest.Entry, proj, localMD5 string, remoteVersions []manifest.RemoteVersion) error {
	latest := latestRemoteVersionInfo(remoteVersions)
	return fmt.Errorf(
		"divergence on %s — both local and remote changed since the pinned baseline\n"+
			"  baseline: v%d  md5 %s   (last synced)\n"+
			"  local:        md5 %s   (changed)\n"+
			"  remote:   v%d  md5 %s   (changed)\n"+
			"Both sides have unreconciled changes; refusing to overwrite either automatically.\n"+
			"Resolve explicitly:\n"+
			"  gosf sync --resolve=theirs   # take remote (discards local)\n"+
			"  gosf sync --resolve=ours     # take local  (discards remote v%d)\n"+
			"or inspect the remote first:  gosf pull %s:%s scratch-copy",
		entry.Local,
		entry.Version, shortMD5(entry.MD5),
		shortMD5(localMD5),
		latest.Version, shortMD5(latest.MD5),
		latest.Version,
		proj, entry.Remote,
	)
}

// shortMD5 renders an MD5 for diagnostics, tolerating an empty value.
func shortMD5(md5 string) string {
	if md5 == "" {
		return "(none)"
	}
	return md5
}
