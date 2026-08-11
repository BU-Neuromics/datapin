package workspace_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/workspace"
	"github.com/BU-Neuromics/datapin/internal/workspace/localdir"
)

func newWS(t *testing.T) *workspace.Workspace {
	t.Helper()
	store, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return workspace.New(store)
}

func md5hex(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

func push(t *testing.T, ws *workspace.Workspace, key string, content []byte) workspace.Event {
	t.Helper()
	ev, err := ws.Push(context.Background(), key, bytes.NewReader(content), int64(len(content)), md5hex(content), "tester")
	if err != nil {
		t.Fatalf("Push(%s): %v", key, err)
	}
	return ev
}

func get(t *testing.T, ws *workspace.Workspace, key string, seq int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := ws.GetVersion(context.Background(), key, seq, &buf); err != nil {
		t.Fatalf("GetVersion(%s, %d): %v", key, seq, err)
	}
	return buf.Bytes()
}

func TestPushAndCurrentBytes(t *testing.T) {
	ws := newWS(t)
	ev := push(t, ws, "results/data.csv", []byte("v1 bytes"))
	if ev.Seq != 1 || ev.Action != "push" || ev.MD5 != md5hex([]byte("v1 bytes")) {
		t.Errorf("event = %+v", ev)
	}
	// Current bytes live at the plain key (browsable layout).
	if got := get(t, ws, "results/data.csv", 0); string(got) != "v1 bytes" {
		t.Errorf("current = %q", got)
	}
}

func TestPushIsIdempotent(t *testing.T) {
	ws := newWS(t)
	push(t, ws, "a.txt", []byte("same"))
	ev, err := ws.Push(context.Background(), "a.txt", bytes.NewReader([]byte("same")), 4, md5hex([]byte("same")), "tester")
	if err != nil {
		t.Fatalf("idempotent push: %v", err)
	}
	if !ev.NoOp {
		t.Error("re-pushing identical content must be a journaled no-op")
	}
	events, err := ws.Versions(context.Background(), "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("events = %+v, want just the original push", events)
	}
}

func TestEveryVersionDatapinWroteIsRevertible(t *testing.T) {
	// The D4 invariant, stated as a test.
	ws := newWS(t)
	v1 := []byte("version one")
	v2 := []byte("version two -- different")
	v3 := []byte("version three")
	push(t, ws, "data.bin", v1)
	push(t, ws, "data.bin", v2)
	push(t, ws, "data.bin", v3)

	events, err := ws.Versions(context.Background(), "data.bin")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	for i, want := range [][]byte{v1, v2, v3} {
		if !events[i].Recoverable {
			t.Errorf("version %d not recoverable", i+1)
		}
		if got := get(t, ws, "data.bin", events[i].Seq); !bytes.Equal(got, want) {
			t.Errorf("version %d bytes = %q, want %q", i+1, got, want)
		}
	}
}

func TestRevertIsAJournaledNewVersion(t *testing.T) {
	ws := newWS(t)
	v1 := []byte("original")
	push(t, ws, "data.bin", v1)
	push(t, ws, "data.bin", []byte("regretted change"))

	ev, err := ws.Revert(context.Background(), "data.bin", 1, "tester", "bad batch")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if ev.Action != "revert" || ev.To != 1 || ev.Reason != "bad batch" {
		t.Errorf("revert event = %+v", ev)
	}
	// Current bytes are v1's again...
	if got := get(t, ws, "data.bin", 0); !bytes.Equal(got, v1) {
		t.Errorf("after revert current = %q", got)
	}
	// ...and history never rewrites: 3 events, the regretted one intact.
	events, _ := ws.Versions(context.Background(), "data.bin")
	if len(events) != 3 {
		t.Fatalf("history rewritten: %+v", events)
	}
	if got := get(t, ws, "data.bin", 2); string(got) != "regretted change" {
		t.Errorf("regretted version lost: %q", got)
	}
}

func TestOutOfBandOverwriteDetectedAndPreserved(t *testing.T) {
	store, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.New(store)
	push(t, ws, "data.bin", []byte("tracked"))

	// Someone scp's over the file behind datapin's back.
	if err := store.Put(context.Background(), "data.bin", strings.NewReader("out of band"), 11); err != nil {
		t.Fatal(err)
	}

	// The next push archives the out-of-band bytes rather than losing them,
	// and the journal records what happened.
	push(t, ws, "data.bin", []byte("next tracked"))
	events, _ := ws.Versions(context.Background(), "data.bin")
	last := events[len(events)-1]
	if !last.OutOfBandOverwrite {
		t.Errorf("out-of-band overwrite not flagged: %+v", last)
	}
	// The out-of-band content is still retrievable through its archive.
	var buf bytes.Buffer
	if err := ws.GetBlob(context.Background(), "data.bin", md5hex([]byte("out of band")), &buf); err != nil {
		t.Errorf("out-of-band bytes lost: %v", err)
	}
}

func TestGCReclaimsOldVersionsAndReportsUnrecoverable(t *testing.T) {
	ws := newWS(t)
	contents := [][]byte{[]byte("v1"), []byte("v2"), []byte("v3"), []byte("v4")}
	for _, c := range contents {
		push(t, ws, "data.bin", c)
	}
	freed, err := ws.GC(context.Background(), 1) // keep 1 archived version besides current
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if freed == 0 {
		t.Error("GC freed nothing")
	}
	events, _ := ws.Versions(context.Background(), "data.bin")
	if len(events) != 4 {
		t.Fatalf("GC must never rewrite the journal: %+v", events)
	}
	// v4 (current) and v3 (kept) recoverable; v1/v2 reclaimed.
	if events[3].Recoverable != true || events[2].Recoverable != true {
		t.Errorf("recent versions must survive GC: %+v", events)
	}
	if events[0].Recoverable || events[1].Recoverable {
		t.Errorf("reclaimed versions must report unrecoverable: %+v", events)
	}
	if err := ws.GetVersion(context.Background(), "data.bin", 1, &bytes.Buffer{}); err == nil {
		t.Error("reading a reclaimed version must error with the reason")
	}
}

func TestListTrackedIgnoresJournalPrefix(t *testing.T) {
	ws := newWS(t)
	push(t, ws, "a.txt", []byte("a"))
	push(t, ws, "sub/b.txt", []byte("b"))
	objs, err := ws.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Errorf("List = %v, want the 2 plain keys only (no .datapin/ internals)", objs)
	}
}

func TestConcurrentSeqAllocationDoesNotClobber(t *testing.T) {
	// Two pushes racing on the same key must yield two distinct journal
	// entries (the naming-race retry), never a silently lost event.
	ws := newWS(t)
	push(t, ws, "a.txt", []byte("one"))
	push(t, ws, "a.txt", []byte("two"))
	push(t, ws, "a.txt", []byte("three"))
	events, _ := ws.Versions(context.Background(), "a.txt")
	seqs := map[int]bool{}
	for _, e := range events {
		if seqs[e.Seq] {
			t.Fatalf("duplicate seq in journal: %+v", events)
		}
		seqs[e.Seq] = true
	}
}
