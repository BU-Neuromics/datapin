package output

import (
	"bytes"
	"encoding/json"
	"testing"
)

// roundTrip marshals v via PrintJSON and unmarshals into a generic map so we
// can assert on the exact JSON field names and values scripting clients see.
func roundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := PrintJSON(&buf, v); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v\njson was: %s", err, buf.String())
	}
	return out
}

func TestOpenResultJSON(t *testing.T) {
	m := roundTrip(t, OpenResult{URL: "https://osf.io/abc12/"})
	if m["url"] != "https://osf.io/abc12/" {
		t.Errorf("url field = %v", m["url"])
	}
}

func TestRemoveResultJSON(t *testing.T) {
	m := roundTrip(t, RemoveResult{Node: "abc12", Path: "/data/x.csv", Kind: "file", DryRun: true})
	if m["node"] != "abc12" || m["path"] != "/data/x.csv" || m["kind"] != "file" {
		t.Errorf("unexpected fields: %+v", m)
	}
	if m["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", m["dry_run"])
	}
}

func TestPullResultJSON(t *testing.T) {
	r := PullResult{
		Downloaded: []TransferItem{{Path: "out.csv", Size: 10}},
		DryRun:     false,
	}
	m := roundTrip(t, r)
	if m["dry_run"] != false {
		t.Errorf("dry_run = %v", m["dry_run"])
	}
	dl, ok := m["downloaded"].([]any)
	if !ok || len(dl) != 1 {
		t.Fatalf("downloaded = %v", m["downloaded"])
	}
	first := dl[0].(map[string]any)
	if first["path"] != "out.csv" || first["size"].(float64) != 10 {
		t.Errorf("transfer item = %+v", first)
	}
}

func TestPushResultJSON(t *testing.T) {
	r := PushResult{
		Uploaded: []TransferItem{{Path: "/data/x.csv", Action: "upload"}},
		DryRun:   false,
	}
	m := roundTrip(t, r)
	up, ok := m["uploaded"].([]any)
	if !ok || len(up) != 1 {
		t.Fatalf("uploaded = %v", m["uploaded"])
	}
	first := up[0].(map[string]any)
	if first["path"] != "/data/x.csv" || first["action"] != "upload" {
		t.Errorf("transfer item = %+v", first)
	}
}

func TestAddResultJSON(t *testing.T) {
	r := AddResult{
		Entries: []AddEntry{
			{Local: "data/counts.h5", Remote: "/data/counts.h5", Project: "abc12", Version: 3, MD5: "deadbeef"},
		},
		ManifestCreated: false,
	}
	m := roundTrip(t, r)
	entries, ok := m["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %v", m["entries"])
	}
	e := entries[0].(map[string]any)
	if e["local"] != "data/counts.h5" {
		t.Errorf("local = %v", e["local"])
	}
	if e["version"].(float64) != 3 {
		t.Errorf("version = %v", e["version"])
	}
	if e["md5"] != "deadbeef" {
		t.Errorf("md5 = %v", e["md5"])
	}
}

func TestAddResult_NotYetPushed(t *testing.T) {
	r := AddResult{
		Entries:         []AddEntry{{Local: "data/new.h5", Remote: "/data/new.h5", Project: "abc12"}},
		ManifestCreated: true,
	}
	m := roundTrip(t, r)
	if m["manifest_created"] != true {
		t.Errorf("manifest_created = %v, want true", m["manifest_created"])
	}
	entries := m["entries"].([]any)
	e := entries[0].(map[string]any)
	if e["version"].(float64) != 0 {
		t.Errorf("version = %v, want 0", e["version"])
	}
}

func TestVersionsResultJSON(t *testing.T) {
	r := NewVersionsResult()
	r.Versions = append(r.Versions, VersionItem{
		Version:     3,
		DateCreated: "2024-03-01T00:00:00",
		Size:        300,
		Contributor: "ada@example.com",
	})
	m := roundTrip(t, r)
	versions, ok := m["versions"].([]any)
	if !ok || len(versions) != 1 {
		t.Fatalf("versions = %v", m["versions"])
	}
	first := versions[0].(map[string]any)
	if first["version"].(float64) != 3 {
		t.Errorf("version = %v, want 3", first["version"])
	}
	if first["contributor"] != "ada@example.com" {
		t.Errorf("contributor = %v", first["contributor"])
	}
}

func TestVersionsResultEmptyIsArray(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintJSON(&buf, NewVersionsResult()); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	if got := buf.String(); !bytes.Contains([]byte(got), []byte(`"versions": []`)) {
		t.Errorf("empty versions should serialise as [], got: %s", got)
	}
}

// TestStatusAndSyncJSONNilGuards verifies that status and sync JSON output
// serialises as [] (not null) when no entries are present.
func TestStatusAndSyncJSONNilGuards(t *testing.T) {
	// StatusItems
	items := make([]StatusItem, 0)
	var buf bytes.Buffer
	if err := PrintJSON(&buf, items); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	if got := buf.String(); !bytes.Contains([]byte(got), []byte("[]")) {
		t.Errorf("empty StatusItem slice should serialise as [], got: %s", got)
	}

	// SyncItems
	buf.Reset()
	syncItems := make([]SyncItem, 0)
	if err := PrintJSON(&buf, syncItems); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	if got := buf.String(); !bytes.Contains([]byte(got), []byte("[]")) {
		t.Errorf("empty SyncItem slice should serialise as [], got: %s", got)
	}
}

// TestEmptyTransferSlicesAreArraysNotNull guards scripting consumers: an empty
// result must serialise as [] rather than null.
func TestEmptyTransferSlicesAreArraysNotNull(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintJSON(&buf, NewPullResult(false)); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	if got := buf.String(); !bytes.Contains([]byte(got), []byte(`"downloaded": []`)) {
		t.Errorf("empty downloaded should serialise as [], got: %s", got)
	}
}

// The status JSON contract is additive-only: the workspace fields added in
// #57 must never appear for an entry that has no workspace, so every
// existing consumer keeps seeing exactly the object it saw before.
func TestStatusItemWorkspaceFieldsAreAdditive(t *testing.T) {
	got := roundTrip(t, StatusItem{Path: "data/x.csv", Kind: "file", State: "IN_SYNC", DeclaredVersion: 2})
	for k, want := range map[string]any{
		"path": "data/x.csv", "kind": "file", "state": "IN_SYNC", "declared_version": float64(2),
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	for _, absent := range []string{"workspace_state", "workspace_remote", "workspace_files"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s must be omitted when unset, got %v", absent, got)
		}
	}

	got = roundTrip(t, StatusItem{
		Path: "counts", Kind: "dataset", State: "IN_SYNC", DeclaredVersion: 1,
		WorkspaceRemote: "nas", WorkspaceState: "BEHIND",
		WorkspaceFiles: []StatusWorkspaceFile{
			{Local: "r/a.csv", Key: "a.csv", State: "BEHIND", Version: 3},
		},
	})
	if got["workspace_remote"] != "nas" || got["workspace_state"] != "BEHIND" {
		t.Errorf("workspace fields = %v", got)
	}
	files, ok := got["workspace_files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("workspace_files = %v", got["workspace_files"])
	}
	f := files[0].(map[string]any)
	if f["key"] != "a.csv" || f["local"] != "r/a.csv" || f["state"] != "BEHIND" || f["version"] != float64(3) {
		t.Errorf("workspace_files[0] = %v", f)
	}
}
