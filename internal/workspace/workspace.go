// Package workspace implements the mutable, DOI-free remote role of plan
// §4.7: plain files at plain keys, with the "datapin" versioning scheme
// (D4) layered on top of any Store — content-addressed archives under
// .datapin/versions/, an append-only journal under .datapin/journal/,
// crash-safety by idempotence, revert-as-new-version, and default-on
// retention reclaimed by GC.
//
// The invariant, precisely: every version datapin wrote is revertible.
// The journal is evidence, never authority — when the observed object
// disagrees with the journal head, observed state wins and the event
// records the out-of-band overwrite.
package workspace

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

// Prefix is the reserved key prefix for datapin's bookkeeping.
const Prefix = ".datapin"

// ObjectInfo describes one stored object. MD5 is "" when the store
// cannot report it cheaply (SFTP, multipart S3 uploads).
type ObjectInfo struct {
	Size int64
	MD5  string
}

// Store is the minimal flat-key storage surface a workspace driver
// provides. The journal scheme is built on top, so every driver gets
// the same versioning surface for free.
type Store interface {
	// List returns every key except those under Prefix.
	List(ctx context.Context) (map[string]ObjectInfo, error)
	Stat(ctx context.Context, key string) (ObjectInfo, bool, error)
	Get(ctx context.Context, key string, w io.Writer) error
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	// Copy duplicates src to dst server-side where the backend allows
	// (S3 CopyObject); drivers may fall back to read+write.
	Copy(ctx context.Context, src, dst string) error
	Delete(ctx context.Context, key string) error
}

// Event is one journal entry — the narrative of what happened to a key.
type Event struct {
	Seq    int    `json:"seq"`
	Action string `json:"action"` // "push" | "revert"
	Key    string `json:"key"`
	MD5    string `json:"md5"`
	Size   int64  `json:"size"`
	Time   string `json:"time"`
	By     string `json:"by,omitempty"`
	To     int    `json:"to,omitempty"`     // revert: the seq restored
	Reason string `json:"reason,omitempty"` // revert: why
	// OutOfBandOverwrite records that the object found before this write
	// did not match the journal head — something else wrote it.
	OutOfBandOverwrite bool `json:"out_of_band_overwrite,omitempty"`

	// Recoverable is computed by Versions (not stored): the version's
	// bytes are still present (current object or archived blob).
	Recoverable bool `json:"-"`
	// NoOp is set on the returned event when a push matched the current
	// content and nothing was written.
	NoOp bool `json:"-"`
}

// Workspace layers the journal scheme over a Store.
type Workspace struct {
	store Store
	now   func() time.Time
}

// New wraps a Store.
func New(store Store) *Workspace {
	return &Workspace{store: store, now: time.Now}
}

func journalDir(key string) string { return Prefix + "/journal/" + key }
func archiveKey(key, sum string) string {
	return Prefix + "/versions/" + key + "/" + sum
}

// Push writes content to key: archive the superseded object (content-
// addressed — re-running after a crash is a no-op), put the new bytes,
// then append the journal event last, so recovery from any crash is
// "re-run the push".
func (ws *Workspace) Push(ctx context.Context, key string, r io.Reader, size int64, sum, by string) (Event, error) {
	if strings.HasPrefix(key, Prefix+"/") || key == Prefix {
		return Event{}, fmt.Errorf("key %q is reserved for datapin bookkeeping", key)
	}
	events, err := ws.journal(ctx, key)
	if err != nil {
		return Event{}, err
	}
	var head *Event
	if len(events) > 0 {
		head = &events[len(events)-1]
	}

	obj, exists, err := ws.store.Stat(ctx, key)
	if err != nil {
		return Event{}, err
	}

	outOfBand := false
	if exists {
		currentMD5 := obj.MD5
		if currentMD5 == "" {
			// The store cannot report a checksum — hash the object.
			currentMD5, err = ws.hashObject(ctx, key)
			if err != nil {
				return Event{}, fmt.Errorf("hashing current %s: %w", key, err)
			}
		}
		if currentMD5 == sum && head != nil && head.MD5 == sum {
			// Identical content, consistent journal: nothing to do.
			return Event{Seq: head.Seq, Action: "push", Key: key, MD5: sum, NoOp: true}, nil
		}
		if head == nil || head.MD5 != currentMD5 {
			// Observed state disagrees with the journal: an out-of-band
			// write. Observed wins; preserve its bytes too.
			outOfBand = true
		}
		// Archive the superseded object under its content address.
		// Content-addressing makes a crashed run's re-copy a no-op.
		if currentMD5 != sum {
			if err := ws.archiveIfAbsent(ctx, key, currentMD5); err != nil {
				return Event{}, fmt.Errorf("archiving superseded %s: %w", key, err)
			}
		}
	}

	if !exists || obj.MD5 != sum {
		if err := ws.store.Put(ctx, key, r, size); err != nil {
			return Event{}, fmt.Errorf("writing %s: %w", key, err)
		}
	}

	ev := Event{
		Action: "push", Key: key, MD5: sum, Size: size,
		Time: ws.now().UTC().Format(time.RFC3339), By: by,
		OutOfBandOverwrite: outOfBand,
	}
	if err := ws.appendEvent(ctx, key, &ev, events); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// Versions returns the key's journal, oldest first, with recoverability
// computed against what is actually present (evidence, not authority).
func (ws *Workspace) Versions(ctx context.Context, key string) ([]Event, error) {
	events, err := ws.journal(ctx, key)
	if err != nil {
		return nil, err
	}
	var headMD5 string
	if obj, exists, err := ws.store.Stat(ctx, key); err == nil && exists {
		headMD5 = obj.MD5
		if headMD5 == "" {
			headMD5, _ = ws.hashObject(ctx, key)
		}
	}
	for i := range events {
		if events[i].MD5 == headMD5 {
			events[i].Recoverable = true
			continue
		}
		_, exists, err := ws.store.Stat(ctx, archiveKey(key, events[i].MD5))
		if err == nil && exists {
			events[i].Recoverable = true
		}
	}
	return events, nil
}

// GetVersion streams a version's bytes: seq 0 means current.
func (ws *Workspace) GetVersion(ctx context.Context, key string, seq int, w io.Writer) error {
	if seq == 0 {
		return ws.store.Get(ctx, key, w)
	}
	events, err := ws.journal(ctx, key)
	if err != nil {
		return err
	}
	var target *Event
	for i := range events {
		if events[i].Seq == seq {
			target = &events[i]
		}
	}
	if target == nil {
		return fmt.Errorf("%s has no version %d", key, seq)
	}
	return ws.GetBlob(ctx, key, target.MD5, w)
}

// GetBlob streams the bytes with the given content address: the current
// object when it matches, else the archived copy.
func (ws *Workspace) GetBlob(ctx context.Context, key, sum string, w io.Writer) error {
	if obj, exists, err := ws.store.Stat(ctx, key); err == nil && exists {
		current := obj.MD5
		if current == "" {
			current, _ = ws.hashObject(ctx, key)
		}
		if current == sum {
			return ws.store.Get(ctx, key, w)
		}
	}
	ak := archiveKey(key, sum)
	if _, exists, err := ws.store.Stat(ctx, ak); err != nil || !exists {
		return fmt.Errorf("%s @ %s is unrecoverable: its bytes are neither current nor archived (reclaimed by gc, or written out-of-band before datapin could archive them)", key, sum)
	}
	// Verify the restored stream — a torn archive must never pass silently.
	h := md5.New()
	if err := ws.store.Get(ctx, ak, io.MultiWriter(w, h)); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("archived %s @ %s is corrupt: stream hashed %s", key, sum, got)
	}
	return nil
}

// Revert restores the bytes of an earlier version as a NEW journaled
// event — history never rewrites.
func (ws *Workspace) Revert(ctx context.Context, key string, toSeq int, by, reason string) (Event, error) {
	events, err := ws.journal(ctx, key)
	if err != nil {
		return Event{}, err
	}
	var target *Event
	for i := range events {
		if events[i].Seq == toSeq {
			target = &events[i]
		}
	}
	if target == nil {
		return Event{}, fmt.Errorf("%s has no version %d", key, toSeq)
	}

	// Archive the current object first (same rule as push).
	if obj, exists, err := ws.store.Stat(ctx, key); err == nil && exists {
		current := obj.MD5
		if current == "" {
			current, _ = ws.hashObject(ctx, key)
		}
		if current == target.MD5 {
			// Already those bytes: journal the revert, move nothing.
		} else {
			if err := ws.archiveIfAbsent(ctx, key, current); err != nil {
				return Event{}, err
			}
			if err := ws.store.Copy(ctx, archiveKey(key, target.MD5), key); err != nil {
				return Event{}, fmt.Errorf("restoring %s @ %s: %w", key, target.MD5, err)
			}
		}
	} else {
		if err := ws.store.Copy(ctx, archiveKey(key, target.MD5), key); err != nil {
			return Event{}, fmt.Errorf("restoring %s @ %s: %w", key, target.MD5, err)
		}
	}

	ev := Event{
		Action: "revert", Key: key, MD5: target.MD5, Size: target.Size,
		Time: ws.now().UTC().Format(time.RFC3339), By: by,
		To: toSeq, Reason: reason,
	}
	if err := ws.appendEvent(ctx, key, &ev, events); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// GC reclaims archived blobs, keeping the `keep` most recent distinct
// archived versions per key (the current object never counts against
// the budget). The journal is never rewritten — reclaimed versions
// simply report unrecoverable. Returns bytes freed.
func (ws *Workspace) GC(ctx context.Context, keep int) (int64, error) {
	keys, err := ws.journalKeys(ctx)
	if err != nil {
		return 0, err
	}
	var freed int64
	for _, key := range keys {
		events, err := ws.journal(ctx, key)
		if err != nil {
			return freed, err
		}
		headMD5 := ""
		if obj, exists, err := ws.store.Stat(ctx, key); err == nil && exists {
			headMD5 = obj.MD5
			if headMD5 == "" {
				headMD5, _ = ws.hashObject(ctx, key)
			}
		}
		// Distinct archived md5s, most recent event first.
		var order []string
		seen := map[string]bool{}
		for i := len(events) - 1; i >= 0; i-- {
			sum := events[i].MD5
			if sum == headMD5 || seen[sum] {
				continue
			}
			seen[sum] = true
			order = append(order, sum)
		}
		for i, sum := range order {
			if i < keep {
				continue
			}
			ak := archiveKey(key, sum)
			if obj, exists, err := ws.store.Stat(ctx, ak); err == nil && exists {
				if err := ws.store.Delete(ctx, ak); err != nil {
					return freed, fmt.Errorf("reclaiming %s: %w", ak, err)
				}
				freed += obj.Size
			}
		}
	}
	return freed, nil
}

// List returns the tracked-facing objects (everything outside Prefix).
func (ws *Workspace) List(ctx context.Context) (map[string]ObjectInfo, error) {
	return ws.store.List(ctx)
}

// Stat exposes the underlying store's Stat for one plain key.
func (ws *Workspace) Stat(ctx context.Context, key string) (ObjectInfo, bool, error) {
	return ws.store.Stat(ctx, key)
}

// Get streams the current bytes of a plain key.
func (ws *Workspace) Get(ctx context.Context, key string, w io.Writer) error {
	return ws.store.Get(ctx, key, w)
}

// HeadMD5 returns the current content address of key ("" if absent),
// hashing when the store cannot report it.
func (ws *Workspace) HeadMD5(ctx context.Context, key string) (string, bool, error) {
	obj, exists, err := ws.store.Stat(ctx, key)
	if err != nil || !exists {
		return "", false, err
	}
	if obj.MD5 != "" {
		return obj.MD5, true, nil
	}
	sum, err := ws.hashObject(ctx, key)
	return sum, true, err
}

// --- internals ---

func (ws *Workspace) archiveIfAbsent(ctx context.Context, key, sum string) error {
	ak := archiveKey(key, sum)
	if _, exists, err := ws.store.Stat(ctx, ak); err == nil && exists {
		return nil // content-addressed: already archived
	}
	return ws.store.Copy(ctx, key, ak)
}

func (ws *Workspace) hashObject(ctx context.Context, key string) (string, error) {
	h := md5.New()
	if err := ws.store.Get(ctx, key, h); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// journal reads a key's events, oldest first.
func (ws *Workspace) journal(ctx context.Context, key string) ([]Event, error) {
	entries, err := ws.listPrefix(ctx, journalDir(key)+"/")
	if err != nil {
		return nil, err
	}
	var events []Event
	for _, entry := range entries {
		var buf strings.Builder
		if err := ws.store.Get(ctx, entry, &buf); err != nil {
			return nil, fmt.Errorf("reading journal entry %s: %w", entry, err)
		}
		var ev Event
		if err := json.Unmarshal([]byte(buf.String()), &ev); err != nil {
			return nil, fmt.Errorf("corrupt journal entry %s: %w", entry, err)
		}
		events = append(events, ev)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	return events, nil
}

// appendEvent writes the event with the next free sequence number.
// A naming collision (concurrent pusher) advances and retries — the
// journal is append-only, never read-modify-write.
func (ws *Workspace) appendEvent(ctx context.Context, key string, ev *Event, known []Event) error {
	next := 1
	if len(known) > 0 {
		next = known[len(known)-1].Seq + 1
	}
	for attempt := 0; attempt < 50; attempt++ {
		ev.Seq = next
		name := fmt.Sprintf("%s/%06d-%s.json", journalDir(key), ev.Seq, ev.MD5)
		if _, exists, err := ws.store.Stat(ctx, name); err == nil && exists {
			next++
			continue
		}
		data, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if err := ws.store.Put(ctx, name, strings.NewReader(string(data)), int64(len(data))); err != nil {
			return fmt.Errorf("appending journal entry: %w", err)
		}
		return nil
	}
	return fmt.Errorf("could not allocate a journal sequence for %s after 50 attempts", key)
}

// journalKeys lists every key that has a journal.
func (ws *Workspace) journalKeys(ctx context.Context) ([]string, error) {
	entries, err := ws.listPrefix(ctx, Prefix+"/journal/")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var keys []string
	for _, e := range entries {
		rel := strings.TrimPrefix(e, Prefix+"/journal/")
		key := path.Dir(rel)
		if key != "." && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// listPrefix lists keys under an internal prefix. Store.List excludes
// Prefix, so this goes through the optional PrefixLister when the driver
// provides one, else walks with Stat probes (drivers all provide it).
func (ws *Workspace) listPrefix(ctx context.Context, prefix string) ([]string, error) {
	pl, ok := ws.store.(PrefixLister)
	if !ok {
		return nil, fmt.Errorf("store %T cannot enumerate internal keys", ws.store)
	}
	return pl.ListPrefix(ctx, prefix)
}

// PrefixLister enumerates keys under an arbitrary prefix (including the
// reserved one). All bundled drivers implement it.
type PrefixLister interface {
	ListPrefix(ctx context.Context, prefix string) ([]string, error)
}
