package workspace

import "context"

// KeyState is how one tracked key's local copy stands against the
// workspace remote. It is the workspace analogue of
// manifest.FileState — but deliberately a smaller set, because the
// workspace track has no baseline pin to compare against: the
// manifest's `[[datasets.files]].md5` is the *archive* pin, written by
// `publish` and never by `push`. So the comparison is two-sided (local
// content vs the workspace head) with the journal supplying history,
// rather than the OSF side's three-way L/B/R.
type KeyState string

const (
	// KeyInSync: local content is byte-identical to the workspace head.
	KeyInSync KeyState = "IN_SYNC"
	// KeyNotPushed: the workspace has no object for this key. A
	// non-empty journal means it was removed out of band; either way
	// the remedy is a push.
	KeyNotPushed KeyState = "NOT_PUSHED"
	// KeyMissing: the workspace has the key, the local file does not exist.
	KeyMissing KeyState = "MISSING"
	// KeyBehind: local content equals a version the journal already
	// recorded, and the head has moved past it — someone pushed newer
	// bytes. A pull fast-forwards.
	KeyBehind KeyState = "BEHIND"
	// KeyAhead: local content matches neither the head nor any journaled
	// version, so the workspace has never seen these bytes. Without a
	// baseline this cannot be told apart from a three-way divergence
	// (local edited AND someone else pushed) — callers must name both
	// remedies rather than guess.
	KeyAhead KeyState = "AHEAD"
)

// ClassifyKey reports how a local file stands against one workspace key.
// localMD5 is "" when the local file is absent; headMD5 is "" when the
// workspace has no object at the key. journal is the key's events (any
// order; only the set of recorded content addresses matters).
func ClassifyKey(localMD5, headMD5 string, journal []Event) KeyState {
	switch {
	case headMD5 == "":
		// Nothing on the workspace. Whether the local file exists decides
		// whether there is anything to push, but not the state.
		return KeyNotPushed
	case localMD5 == "":
		return KeyMissing
	case localMD5 == headMD5:
		return KeyInSync
	}
	// The head holds bytes the local copy does not. If the local content is
	// a version the journal already recorded, the local copy is simply
	// older and a pull fast-forwards it. (This also covers an out-of-band
	// overwrite: local may match the *journal* head while the object does
	// not — observed state wins, so the workspace still has bytes we lack.)
	for _, ev := range journal {
		if ev.MD5 == localMD5 {
			return KeyBehind
		}
	}
	return KeyAhead
}

// Journal returns a key's events oldest-first, without computing
// recoverability. Versions is the richer, costlier read (it stats the
// object and every archived blob, which hashes them on stores that
// cannot report a checksum); status only needs the md5 sequence.
func (ws *Workspace) Journal(ctx context.Context, key string) ([]Event, error) {
	return ws.journal(ctx, key)
}
