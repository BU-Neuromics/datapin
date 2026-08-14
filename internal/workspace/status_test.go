package workspace_test

import (
	"context"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/workspace"
)

// ClassifyKey is the workspace analogue of manifest.ClassifyFile: it
// compares local content against the workspace head and the key's journal.
// There is no workspace baseline pin (the manifest's md5 is the archive
// pin, written only by publish), so the comparison is two-sided plus
// history — see the table for exactly what each state asserts.
func TestClassifyKey(t *testing.T) {
	ev := func(seq int, md5 string) workspace.Event {
		return workspace.Event{Seq: seq, Action: "push", MD5: md5}
	}
	tests := []struct {
		name     string
		localMD5 string
		headMD5  string
		journal  []workspace.Event
		want     workspace.KeyState
	}{
		{"nothing anywhere", "", "", nil, workspace.KeyNotPushed},
		{"local only, never pushed", "aaa", "", nil, workspace.KeyNotPushed},
		{"local only, journal says it was pushed then removed out of band",
			"aaa", "", []workspace.Event{ev(1, "aaa")}, workspace.KeyNotPushed},
		{"local absent, workspace has it", "", "aaa", []workspace.Event{ev(1, "aaa")}, workspace.KeyMissing},
		{"identical", "aaa", "aaa", []workspace.Event{ev(1, "aaa")}, workspace.KeyInSync},
		{"identical with no journal (pushed by something else)", "aaa", "aaa", nil, workspace.KeyInSync},
		{"local matches an older version — a newer one was pushed",
			"aaa", "bbb", []workspace.Event{ev(1, "aaa"), ev(2, "bbb")}, workspace.KeyBehind},
		{"local matches the journal head but the object differs (out-of-band write)",
			"bbb", "ccc", []workspace.Event{ev(1, "aaa"), ev(2, "bbb")}, workspace.KeyBehind},
		{"local content the workspace has never seen",
			"zzz", "bbb", []workspace.Event{ev(1, "aaa"), ev(2, "bbb")}, workspace.KeyAhead},
		{"local content unknown, no journal at all",
			"zzz", "bbb", nil, workspace.KeyAhead},
	}
	for _, tt := range tests {
		got := workspace.ClassifyKey(tt.localMD5, tt.headMD5, tt.journal)
		if got != tt.want {
			t.Errorf("%s: ClassifyKey(%q, %q, %d events) = %q, want %q",
				tt.name, tt.localMD5, tt.headMD5, len(tt.journal), got, tt.want)
		}
	}
}

// Journal exposes a key's events without computing recoverability, which
// costs an extra Stat (and, on a hashing store, an extra read) per event.
// Status only needs the md5 sequence.
func TestJournalExposesEventsWithoutRecoverability(t *testing.T) {
	ws := newWS(t)
	push(t, ws, "results/data.csv", []byte("v1"))
	push(t, ws, "results/data.csv", []byte("v2"))

	events, err := ws.Journal(context.Background(), "results/data.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("Journal returned %d events, want 2", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Errorf("events out of order: %+v", events)
	}
	if events[0].MD5 != md5hex([]byte("v1")) || events[1].MD5 != md5hex([]byte("v2")) {
		t.Errorf("md5s = %q, %q", events[0].MD5, events[1].MD5)
	}

	// An unknown key has no journal, and that is not an error.
	events, err = ws.Journal(context.Background(), "nope")
	if err != nil {
		t.Fatalf("Journal on unknown key: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("Journal on unknown key returned %d events", len(events))
	}
}
