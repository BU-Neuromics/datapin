package cmd

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakeosf"
)

func md5of(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h[:])
}

func writeFileHelper(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

func readFileHelper(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func newWikiScanFixture(t *testing.T) (*fakeosf.Server, *client.OSFClient) {
	t.Helper()
	srv := fakeosf.New()
	t.Cleanup(srv.Close)
	t.Setenv("DATAPIN_API_BASE", srv.URL()+"/v2")
	return srv, client.New("")
}

func TestFetchWikiRemoteState_PageAbsent(t *testing.T) {
	srv, c := newWikiScanFixture(t)
	srv.AddProject("abc12", "P")

	entry := manifest.WikiEntry{Local: "docs/home.md", Page: "home"}
	wiki, versions, err := fetchWikiRemoteState(context.Background(), newWikiScanCache(c), c, "abc12", entry, md5of("x"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if wiki != nil || versions != nil {
		t.Errorf("absent page should yield nil wiki and versions, got %v %v", wiki, versions)
	}
}

func TestFetchWikiRemoteState_SkipsHistoryWhenLocalMatchesLatest(t *testing.T) {
	srv, c := newWikiScanFixture(t)
	srv.AddProject("abc12", "P")
	srv.AddWiki("abc12", "home", []byte("v1"))
	srv.AddWikiVersion("abc12", "home", []byte("v2"))

	entry := manifest.WikiEntry{Local: "docs/home.md", Page: "home"}
	wiki, versions, err := fetchWikiRemoteState(context.Background(), newWikiScanCache(c), c, "abc12", entry, md5of("v2"))
	if err != nil || wiki == nil {
		t.Fatalf("err = %v, wiki = %v", err, wiki)
	}
	if len(versions) != 1 || versions[0].Version != 2 || versions[0].MD5 != md5of("v2") {
		t.Errorf("versions = %+v", versions)
	}
	if srv.WikiVersionRequests() != 0 {
		t.Errorf("version list should not be fetched on the skip path, got %d requests", srv.WikiVersionRequests())
	}
	if srv.WikiVersionContentRequests() != 0 {
		t.Errorf("no historical content should be fetched, got %d requests", srv.WikiVersionContentRequests())
	}

	// The classification matches the file semantics: unpinned + identical → PIN_ONLY.
	state := manifest.ClassifyFile(entry.BaselineEntry(), md5of("v2"), versions, false)
	if state != manifest.StatePinOnly {
		t.Errorf("state = %v, want PIN_ONLY", state)
	}
}

func TestFetchWikiRemoteState_SkipsHistoryWhenPinnedBaselineHolds(t *testing.T) {
	srv, c := newWikiScanFixture(t)
	srv.AddProject("abc12", "P")
	srv.AddWiki("abc12", "home", []byte("v1"))
	srv.AddWikiVersion("abc12", "home", []byte("v2"))
	srv.AddWikiVersion("abc12", "home", []byte("v3"))

	// Pinned at v1 and local still equals the baseline → REMOTE_NEWER is
	// decidable from the latest version alone.
	entry := manifest.WikiEntry{Local: "docs/home.md", Page: "home", Version: 1, MD5: md5of("v1")}
	_, versions, err := fetchWikiRemoteState(context.Background(), newWikiScanCache(c), c, "abc12", entry, md5of("v1"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(versions) != 1 || versions[0].Version != 3 {
		t.Errorf("versions = %+v", versions)
	}
	if srv.WikiVersionContentRequests() != 0 {
		t.Errorf("no historical content should be fetched, got %d", srv.WikiVersionContentRequests())
	}
	state := manifest.ClassifyFile(entry.BaselineEntry(), md5of("v1"), versions, false)
	if state != manifest.StateRemoteNewer {
		t.Errorf("state = %v, want REMOTE_NEWER", state)
	}
}

func TestFetchWikiRemoteState_FullHistoryWhenDiverging(t *testing.T) {
	srv, c := newWikiScanFixture(t)
	srv.AddProject("abc12", "P")
	srv.AddWiki("abc12", "home", []byte("v1"))
	srv.AddWikiVersion("abc12", "home", []byte("v2"))
	srv.AddWikiVersion("abc12", "home", []byte("v3"))

	// Local content is the old v1, pinned at v2: matches neither baseline nor
	// latest, so the full history must be hashed to detect BEHIND.
	entry := manifest.WikiEntry{Local: "docs/home.md", Page: "home", Version: 2, MD5: md5of("v2")}
	_, versions, err := fetchWikiRemoteState(context.Background(), newWikiScanCache(c), c, "abc12", entry, md5of("v1"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("versions = %+v", versions)
	}
	for _, rv := range versions {
		if rv.MD5 != md5of(fmt.Sprintf("v%d", rv.Version)) {
			t.Errorf("v%d MD5 = %q", rv.Version, rv.MD5)
		}
	}
	// Latest content came from the content endpoint; only the two older
	// versions need historical fetches.
	if srv.WikiVersionContentRequests() != 2 {
		t.Errorf("historical content requests = %d, want 2", srv.WikiVersionContentRequests())
	}
	state := manifest.ClassifyFile(entry.BaselineEntry(), md5of("v1"), versions, false)
	if state != manifest.StateBehind {
		t.Errorf("state = %v, want BEHIND", state)
	}
	// A truly unique local hash instead diverges.
	state = manifest.ClassifyFile(entry.BaselineEntry(), md5of("local edits"), versions, false)
	if state != manifest.StateDivergent {
		t.Errorf("state = %v, want DIVERGED", state)
	}
}

func TestFetchWikiRemoteState_DisabledSurfacesError(t *testing.T) {
	srv, c := newWikiScanFixture(t)
	srv.AddProject("abc12", "P")
	srv.SetWikiDisabled("abc12")

	entry := manifest.WikiEntry{Local: "docs/home.md", Page: "home"}
	_, _, err := fetchWikiRemoteState(context.Background(), newWikiScanCache(c), c, "abc12", entry, md5of("x"))
	if err == nil {
		t.Fatal("expected error for disabled wiki")
	}
}

func TestWikiScanCache_CollapsesListings(t *testing.T) {
	srv, c := newWikiScanFixture(t)
	srv.AddProject("abc12", "P")
	srv.AddWiki("abc12", "home", []byte("v1"))
	srv.AddWiki("abc12", "notes", []byte("n1"))

	cache := newWikiScanCache(c)
	entryA := manifest.WikiEntry{Local: "a.md", Page: "home"}
	entryB := manifest.WikiEntry{Local: "b.md", Page: "notes"}
	if _, _, err := fetchWikiRemoteState(context.Background(), cache, c, "abc12", entryA, md5of("v1")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fetchWikiRemoteState(context.Background(), cache, c, "abc12", entryB, md5of("n1")); err != nil {
		t.Fatal(err)
	}
	if n := srv.WikiListRequests(); n != 1 {
		t.Errorf("wiki list requests = %d, want 1 (memoized)", n)
	}
}
