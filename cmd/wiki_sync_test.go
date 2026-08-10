package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakeosf"
)

// wikiPlanFor builds the classified plan executeWikiEntry consumes.
func wikiPlanFor(we *manifest.WikiEntry, state manifest.FileState, localAbs, localMD5 string, page *client.Wiki, versions []manifest.RemoteVersion) wikiEntryPlan {
	return wikiEntryPlan{
		entry: we, proj: "abc12", localAbs: localAbs, localMD5: localMD5,
		state: state, page: page, remoteVersions: versions,
	}
}

// --- no-transfer / refusal paths: nil client is safe ---

func TestExecuteWikiEntry_PinOnly(t *testing.T) {
	we := &manifest.WikiEntry{Local: "docs/home.md", Page: "home"}
	versions := []manifest.RemoteVersion{{Version: 2, MD5: "aaa"}}
	p := wikiPlanFor(we, manifest.StatePinOnly, "/tmp/x", "aaa", nil, versions)

	action, changed, err := executeWikiEntry(context.Background(), nil, p, actionPin, false)
	if err != nil || !changed || action != "pinned" {
		t.Fatalf("action=%q changed=%v err=%v", action, changed, err)
	}
	if we.Version != 2 || we.MD5 != "aaa" {
		t.Errorf("pinned to v%d/%s", we.Version, we.MD5)
	}
}

// A wiki page whose remote moved ahead fast-forwards; it is never pushed back
// over the newer version by a plain sync.
func TestSyncDecision_WikiRemoteNewerFastForwards(t *testing.T) {
	if got := syncDecision(manifest.StateRemoteNewer, true, false, ""); got != actionPull {
		t.Errorf("REMOTE_NEWER = %v, want actionPull (fast-forward, not rollback refusal)", got)
	}
}

func TestExecuteWikiEntry_DivergedFailsHard(t *testing.T) {
	we := &manifest.WikiEntry{Local: "docs/home.md", Page: "home", Version: 1, MD5: "B"}
	versions := []manifest.RemoteVersion{{Version: 2, MD5: "R"}, {Version: 1, MD5: "B"}}
	p := wikiPlanFor(we, manifest.StateDivergent, "/tmp/x", "L", nil, versions)

	_, _, err := executeWikiEntry(context.Background(), nil, p, actionBlocked, false)
	if err == nil || !strings.Contains(err.Error(), "divergence") {
		t.Fatalf("expected divergence error, got %v", err)
	}
}

func TestExecuteWikiEntry_AheadIsReported(t *testing.T) {
	we := &manifest.WikiEntry{Local: "docs/home.md", Page: "home", Version: 1, MD5: "B"}
	versions := []manifest.RemoteVersion{{Version: 1, MD5: "B"}}
	p := wikiPlanFor(we, manifest.StateAheadOfManifest, "/tmp/x", "L", nil, versions)

	action, changed, err := executeWikiEntry(context.Background(), nil, p, actionReport, false)
	if err != nil || changed || !strings.HasPrefix(action, "skipped") {
		t.Fatalf("action=%q changed=%v err=%v", action, changed, err)
	}
}

// --- transfer paths against fakeosf ---

func wikiSyncEnv(t *testing.T) (*fakeosf.Server, *client.OSFClient, string) {
	t.Helper()
	srv := fakeosf.New()
	t.Cleanup(srv.Close)
	t.Setenv("DATAPIN_API_BASE", srv.URL()+"/v2")
	dir := t.TempDir()
	return srv, client.New("tok"), dir
}

func TestExecuteWikiEntry_CreatesPage(t *testing.T) {
	srv, c, dir := wikiSyncEnv(t)
	srv.AddProject("abc12", "P")

	local := filepath.Join(dir, "home.md")
	if err := writeFileHelper(local, "new content\n"); err != nil {
		t.Fatal(err)
	}
	we := &manifest.WikiEntry{Local: "home.md", Page: "home"}
	p := wikiPlanFor(we, manifest.StateNotPushed, local, md5of("new content\n"), nil, nil)

	action, changed, err := executeWikiEntry(context.Background(), c, p, actionPush, false)
	if err != nil || !changed {
		t.Fatalf("action=%q changed=%v err=%v", action, changed, err)
	}
	if page := srv.GetWiki("abc12", "home"); page == nil || string(page.LatestContent()) != "new content" {
		t.Errorf("page not created with expected (canonical) content")
	}
	if we.Version != 1 || we.MD5 != md5of("new content\n") {
		t.Errorf("entry pinned to v%d/%s", we.Version, we.MD5)
	}
}

func TestExecuteWikiEntry_NewVersion(t *testing.T) {
	srv, c, dir := wikiSyncEnv(t)
	srv.AddProject("abc12", "P")
	page := srv.AddWiki("abc12", "home", []byte("v1\n"))

	local := filepath.Join(dir, "home.md")
	if err := writeFileHelper(local, "v2\n"); err != nil {
		t.Fatal(err)
	}
	wiki := &client.Wiki{ID: page.ID, Attributes: client.WikiAttributes{Name: "home", Extra: client.WikiExtra{Version: 1}}}
	we := &manifest.WikiEntry{Local: "home.md", Page: "home", Version: 1, MD5: md5of("v1\n")}
	p := wikiPlanFor(we, manifest.StateAheadOfManifest, local, md5of("v2\n"), wiki,
		[]manifest.RemoteVersion{{Version: 1, MD5: md5of("v1\n")}})

	action, changed, err := executeWikiEntry(context.Background(), c, p, actionPush, false)
	if err != nil || !changed {
		t.Fatalf("action=%q changed=%v err=%v", action, changed, err)
	}
	if we.Version != 2 || we.MD5 != md5of("v2\n") {
		t.Errorf("entry pinned to v%d/%s", we.Version, we.MD5)
	}
	if v := srv.GetWiki("abc12", "home").LatestVersion(); v != 2 {
		t.Errorf("server version = %d", v)
	}
}

func TestExecuteWikiEntry_FastForward(t *testing.T) {
	srv, c, dir := wikiSyncEnv(t)
	srv.AddProject("abc12", "P")
	page := srv.AddWiki("abc12", "home", []byte("v1\n"))
	srv.AddWikiVersion("abc12", "home", []byte("v2 remote\n"))

	local := filepath.Join(dir, "home.md")
	if err := writeFileHelper(local, "v1\n"); err != nil {
		t.Fatal(err)
	}
	wiki := &client.Wiki{ID: page.ID, Attributes: client.WikiAttributes{Name: "home", Extra: client.WikiExtra{Version: 2}}}
	we := &manifest.WikiEntry{Local: "home.md", Page: "home", Version: 1, MD5: md5of("v1\n")}
	p := wikiPlanFor(we, manifest.StateRemoteNewer, local, md5of("v1"), wiki,
		[]manifest.RemoteVersion{{Version: 2, MD5: md5of("v2 remote")}})

	action, changed, err := executeWikiEntry(context.Background(), c, p, actionPull, false)
	if err != nil || !changed {
		t.Fatalf("action=%q changed=%v err=%v", action, changed, err)
	}
	// Pulled content is the canonical form OSF stored (trailing newline trimmed).
	if got := readFileHelper(t, local); got != "v2 remote" {
		t.Errorf("local content = %q", got)
	}
	if we.Version != 2 || we.MD5 != md5of("v2 remote") {
		t.Errorf("entry pinned to v%d/%s", we.Version, we.MD5)
	}
}

// The bug in #81, for wikis: a page that exists on the remote but not locally
// is written by a plain sync, with no flag at all.
func TestExecuteWikiEntry_Missing_WritesFile(t *testing.T) {
	srv, c, dir := wikiSyncEnv(t)
	srv.AddProject("abc12", "P")
	page := srv.AddWiki("abc12", "home", []byte("only version\n"))

	local := filepath.Join(dir, "sub", "home.md") // does not exist yet
	wiki := &client.Wiki{ID: page.ID, Attributes: client.WikiAttributes{Name: "home", Extra: client.WikiExtra{Version: 1}}}
	we := &manifest.WikiEntry{Local: "sub/home.md", Page: "home"}
	p := wikiPlanFor(we, manifest.StateMissing, local, "", wiki,
		[]manifest.RemoteVersion{{Version: 1, MD5: md5of("only version")}})

	act := syncDecision(p.state, false, false, "")
	if act != actionPull {
		t.Fatalf("a missing wiki page must be pulled with no flags, got %v", act)
	}
	action, changed, err := executeWikiEntry(context.Background(), c, p, act, false)
	if err != nil || !changed {
		t.Fatalf("action=%q changed=%v err=%v", action, changed, err)
	}
	if got := readFileHelper(t, local); got != "only version" {
		t.Errorf("local content = %q", got)
	}
	if we.Version != 1 {
		t.Errorf("entry should be pinned after restore, got v%d", we.Version)
	}
}

func TestExecuteWikiEntry_DryRunNoWrite(t *testing.T) {
	srv, c, dir := wikiSyncEnv(t)
	srv.AddProject("abc12", "P")
	local := filepath.Join(dir, "home.md")
	if err := writeFileHelper(local, "x\n"); err != nil {
		t.Fatal(err)
	}
	we := &manifest.WikiEntry{Local: "home.md", Page: "home"}
	p := wikiPlanFor(we, manifest.StateNotPushed, local, md5of("x\n"), nil, nil)

	_, changed, err := executeWikiEntry(context.Background(), c, p, actionPush, true /*dryRun*/)
	if err != nil || changed {
		t.Fatalf("dry-run: changed=%v err=%v", changed, err)
	}
	if srv.GetWiki("abc12", "home") != nil {
		t.Error("dry-run must not create the page")
	}
}
