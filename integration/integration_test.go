//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/testutil/fakeosf"
)

// binaryPath is set once in TestMain and shared across all tests.
var binaryPath string

func TestMain(m *testing.M) {
	bin, err := buildBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: build binary: %v\n", err)
		os.Exit(1)
	}
	binaryPath = bin
	os.Exit(m.Run())
}

func buildBinary() (string, error) {
	// When running "go test ./integration/", the working dir is integration/.
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	repoRoot, err := filepath.Abs(filepath.Join(wd, ".."))
	if err != nil {
		return "", err
	}

	tmp, err := os.MkdirTemp("", "gosf-integ-*")
	if err != nil {
		return "", err
	}

	bin := filepath.Join(tmp, "gosf")
	buildArgs := []string{"build", "-o", bin, "."}
	if os.Getenv("GOSF_COVERDIR") != "" {
		// Instrument the binary so subprocess runs emit coverage into GOCOVERDIR.
		buildArgs = []string{"build", "-cover", "-o", bin, "."}
	}
	cmd := exec.Command("go", buildArgs...)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build failed: %s", out)
	}
	return bin, nil
}

// coverEnv returns a GOCOVERDIR entry for the subprocess when GOSF_COVERDIR is
// set, so an instrumented binary writes its coverage there; empty otherwise.
func coverEnv() []string {
	if dir := os.Getenv("GOSF_COVERDIR"); dir != "" {
		return []string{"GOCOVERDIR=" + dir}
	}
	return nil
}

// testEnv wraps a temp working directory and fake server for one test.
type testEnv struct {
	t   *testing.T
	srv *fakeosf.Server
	dir string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	srv := fakeosf.New()
	t.Cleanup(srv.Close)
	return &testEnv{t: t, srv: srv, dir: t.TempDir()}
}

// run executes gosf with args in the test directory and returns stdout, stderr, exit code.
func (e *testEnv) run(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(),
		// The OSF client appends "/nodes/...", "/files/..." etc. to the base,
		// so we must include the /v2 prefix that the real URL carries.
		"GOSF_API_BASE="+e.srv.URL()+"/v2",
		// The Waterbutler client uses the full path, so no suffix needed.
		"GOSF_FILES_BASE="+e.srv.URL(),
		"OSF_TOKEN=test-token",
		// Isolate from the developer's real keychain and config.
		"HOME="+e.dir,
		"XDG_CONFIG_HOME="+filepath.Join(e.dir, ".config"),
	)
	cmd.Env = append(cmd.Env, coverEnv()...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	return outBuf.String(), errBuf.String(), code
}

// runClean runs gosf like run() but with NO forced OSF_TOKEN (it is cleared) and
// optional stdin, so auth tests fully control the token source. Storage stays
// isolated in the test's temp HOME/XDG dir.
func (e *testEnv) runClean(stdin string, args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(),
		"GOSF_API_BASE="+e.srv.URL()+"/v2",
		"GOSF_FILES_BASE="+e.srv.URL(),
		"HOME="+e.dir,
		"XDG_CONFIG_HOME="+filepath.Join(e.dir, ".config"),
		"OSF_TOKEN=", // clear any inherited token; auth tests set their own source
	)
	cmd.Env = append(cmd.Env, coverEnv()...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	return outBuf.String(), errBuf.String(), code
}

// writeFile creates a file (and any parent dirs) under the test directory.
func (e *testEnv) writeFile(name, content string) {
	e.t.Helper()
	p := filepath.Join(e.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		e.t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		e.t.Fatalf("write %s: %v", name, err)
	}
}

// readFile reads a file from the test directory.
func (e *testEnv) readFile(name string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(name)))
	if err != nil {
		e.t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// fileExists reports whether name exists inside the test directory.
func (e *testEnv) fileExists(name string) bool {
	_, err := os.Stat(filepath.Join(e.dir, filepath.FromSlash(name)))
	return err == nil
}

// ---- Pull ----

func TestPull_SingleFile(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/results.csv", []byte("col1,col2\n1,2\n"))

	_, stderr, code := env.run("pull", "abc12:/results.csv", "--quiet")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if !env.fileExists("results.csv") {
		t.Fatal("results.csv not created")
	}
	if got := env.readFile("results.csv"); got != "col1,col2\n1,2\n" {
		t.Errorf("content = %q", got)
	}
}

func TestPull_FolderTree(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/a.csv", []byte("aaa"))
	env.srv.AddFile("abc12", "/data/b.csv", []byte("bbb"))

	_, stderr, code := env.run("pull", "abc12:/data/", "out/", "--quiet")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if got := env.readFile("out/a.csv"); got != "aaa" {
		t.Errorf("a.csv = %q", got)
	}
	if got := env.readFile("out/b.csv"); got != "bbb" {
		t.Errorf("b.csv = %q", got)
	}
}

func TestPull_DryRun(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/report.csv", []byte("data"))

	stdout, stderr, code := env.run("pull", "abc12:/report.csv", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if env.fileExists("report.csv") {
		t.Fatal("dry-run should not create file")
	}
	if stdout != "" {
		t.Errorf("text-mode dry-run should print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "report.csv") {
		t.Errorf("expected filename in dry-run activity on stderr; got %q", stderr)
	}
}

func TestPull_AutoTracksFile(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("h5 content"))
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")

	_, stderr, code := env.run("pull", "abc12:/data/counts.h5", "--quiet")
	if code != 0 {
		t.Fatalf("pull exit %d; stderr=%s", code, stderr)
	}
	if !env.fileExists("data/counts.h5") {
		t.Fatal("file not downloaded")
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "/data/counts.h5") {
		t.Errorf("expected manifest entry for /data/counts.h5:\n%s", toml)
	}
	if !strings.Contains(toml, "version = 1") {
		t.Errorf("expected the pulled entry to be pinned to v1:\n%s", toml)
	}
	if strings.Contains(toml, "direction") {
		t.Errorf("direction is no longer written to the manifest:\n%s", toml)
	}
}

func TestPull_NoTrack(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("h5 content"))
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")

	_, stderr, code := env.run("pull", "abc12:/data/counts.h5", "--no-track", "--quiet")
	if code != 0 {
		t.Fatalf("pull exit %d; stderr=%s", code, stderr)
	}
	if !env.fileExists("data/counts.h5") {
		t.Fatal("file not downloaded")
	}
	toml := env.readFile(".gosf/gosf.toml")
	if strings.Contains(toml, "counts.h5") {
		t.Errorf("--no-track: manifest should not contain counts.h5:\n%s", toml)
	}
}

func TestPull_AlternateDestinationDownloadsWithoutTracking(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("data"))
	manifestBody := `[project]
id = "abc12"

[[files]]
local = "other/counts.h5"
remote = "/data/counts.h5"
version = 0
md5 = ""
`
	env.writeFile(".gosf/gosf.toml", manifestBody)

	// Pulling a tracked remote path to an explicit alternate destination is a
	// plain download: it must succeed, land the bytes at the requested path,
	// and leave the manifest untouched (issue #36).
	_, stderr, code := env.run("pull", "abc12:/data/counts.h5", "scratch/copy.h5")
	if code != 0 {
		t.Fatalf("expected exit 0 pulling to alternate destination; stderr=%s", stderr)
	}
	if !env.fileExists("scratch/copy.h5") {
		t.Fatal("file was not downloaded to the alternate destination")
	}
	if got := env.readFile("scratch/copy.h5"); got != "data" {
		t.Errorf("downloaded content = %q, want %q", got, "data")
	}
	if !strings.Contains(stderr, "without tracking") {
		t.Errorf("stderr should note the download was not tracked: %s", stderr)
	}
	// The manifest must be unchanged: no new entry for the scratch path.
	toml := env.readFile(".gosf/gosf.toml")
	if strings.Contains(toml, "scratch/copy.h5") {
		t.Errorf("manifest should not have been re-tracked; got:\n%s", toml)
	}
	if !strings.Contains(toml, `local = "other/counts.h5"`) {
		t.Errorf("original manifest entry should be preserved; got:\n%s", toml)
	}
}

func TestPull_BareFollowsManifest(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data/counts.h5", []byte("counts data"))
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local = "data/counts.h5"
remote = "/data/counts.h5"
version = 1
md5 = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("pull", "--quiet")
	if code != 0 {
		t.Fatalf("bare pull exit %d; stderr=%s", code, stderr)
	}
	if !env.fileExists("data/counts.h5") {
		t.Fatal("pull-eligible file not downloaded by bare pull")
	}
}

func TestPull_BareNoProjectID(t *testing.T) {
	env := newTestEnv(t)
	// gosf.toml exists but has no [project].id and no entries.
	env.writeFile(".gosf/gosf.toml", "")

	_, stderr, code := env.run("pull")
	if code == 0 {
		t.Error("expected error for bare pull with no project configured")
	}
	if !strings.Contains(stderr, "gosf init") {
		t.Errorf("error should mention 'gosf init': %s", stderr)
	}
}

// A tracked file that exists locally but nowhere on the remote has nothing for
// a pull to fetch, so bare pull leaves it alone (and does not fail the run).
func TestPull_BareLeavesUnpushedLocalFileAlone(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local = "data/push_only.csv"
remote = "/data/push_only.csv"
version = 0
md5 = ""
`)
	env.writeFile("data/push_only.csv", "data")

	_, _, code := env.run("pull", "--quiet")
	if code != 0 {
		t.Fatalf("bare pull should succeed when an entry has nothing to fetch")
	}
	if env.readFile("data/push_only.csv") != "data" {
		t.Error("bare pull must not touch a file the remote does not have")
	}
}

// ---- Push ----

func TestPush_NewFile(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile("data.csv", "col1,col2\n1,2\n")

	_, stderr, code := env.run("push", "data.csv", "abc12:/data.csv", "--quiet")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	uploads := env.srv.Uploads()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(uploads))
	}
	if string(uploads[0].Content) != "col1,col2\n1,2\n" {
		t.Errorf("upload content = %q", uploads[0].Content)
	}
	if uploads[0].Path != "/data.csv" {
		t.Errorf("upload path = %q, want /data.csv", uploads[0].Path)
	}
}

// TestPush_NewFileIntoSubfolder guards the fix for the subfolder-upload 404:
// uploading a new file into an existing subfolder must target the folder's
// ID-based Waterbutler upload link, not a name-built path (which real OSF 404s).
func TestPush_NewFileIntoSubfolder(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/sub") // folder exists, has an opaque ID
	env.writeFile("x.csv", "hello\n")

	_, stderr, code := env.run("push", "x.csv", "abc12:/sub/x.csv", "--quiet")
	if code != 0 {
		t.Fatalf("push into subfolder failed: exit %d; stderr=%s", code, stderr)
	}
	// The uploaded file must be listed under /sub.
	out, _, _ := env.run("ls", "abc12:/sub", "--output=json")
	if !strings.Contains(out, "x.csv") {
		t.Errorf("pushed file not listed under /sub:\n%s", out)
	}
}

func TestPush_ConflictSkip(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data.csv", []byte("original"))
	env.writeFile("data.csv", "new content")

	_, _, code := env.run("push", "data.csv", "abc12:/data.csv", "--conflict=skip", "--quiet")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// skip: no new upload should have been recorded.
	if uploads := env.srv.Uploads(); len(uploads) != 0 {
		t.Errorf("expected 0 uploads with --conflict=skip, got %d", len(uploads))
	}
}

func TestPush_ConflictOverwrite(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data.csv", []byte("original"))
	env.writeFile("data.csv", "new content")

	_, stderr, code := env.run("push", "data.csv", "abc12:/data.csv", "--conflict=overwrite", "--quiet")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	f := env.srv.GetFile("abc12", "/data.csv")
	if f == nil {
		t.Fatal("file not in server")
	}
	if f.LatestVersion() != 2 {
		t.Errorf("latest version = %d, want 2", f.LatestVersion())
	}
	if string(f.LatestContent()) != "new content" {
		t.Errorf("content = %q", f.LatestContent())
	}
}

func TestPush_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile("report.csv", "x")

	stdout, _, code := env.run("push", "report.csv", "abc12:/report.csv", "--output=json", "--quiet")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	uploaded, ok := result["uploaded"].([]any)
	if !ok || len(uploaded) != 1 {
		t.Fatalf("uploaded = %v", result["uploaded"])
	}
	item := uploaded[0].(map[string]any)
	if item["path"] != "/report.csv" {
		t.Errorf("path = %v", item["path"])
	}
	if item["action"] != "upload" {
		t.Errorf("action = %v", item["action"])
	}
}

func TestPush_AutoTracksFile(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")
	env.writeFile("data.csv", "col1,col2\n1,2\n")

	_, stderr, code := env.run("push", "data.csv", "abc12:/data.csv", "--quiet")
	if code != 0 {
		t.Fatalf("push exit %d; stderr=%s", code, stderr)
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "/data.csv") {
		t.Errorf("expected /data.csv in gosf.toml:\n%s", toml)
	}
	if strings.Contains(toml, "direction") {
		t.Errorf("direction is no longer written to the manifest:\n%s", toml)
	}
	if !strings.Contains(toml, "version = 1") {
		t.Errorf("expected version = 1 in gosf.toml:\n%s", toml)
	}
}

func TestPush_NoTrack(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")
	env.writeFile("data.csv", "content")

	_, stderr, code := env.run("push", "data.csv", "abc12:/data.csv", "--no-track", "--quiet")
	if code != 0 {
		t.Fatalf("push exit %d; stderr=%s", code, stderr)
	}
	toml := env.readFile(".gosf/gosf.toml")
	if strings.Contains(toml, "data.csv") {
		t.Errorf("--no-track: manifest should not contain data.csv:\n%s", toml)
	}
}

func TestPush_DuplicateRemoteConflict(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local = "other/data.csv"
remote = "/data.csv"
version = 0
md5 = ""
`)
	env.writeFile("data.csv", "new content")

	_, stderr, code := env.run("push", "data.csv", "abc12:/data.csv", "--quiet")
	if code == 0 {
		t.Error("expected error when pushing to already-tracked remote from different local path")
	}
	if !strings.Contains(stderr, "already tracked") {
		t.Errorf("stderr should mention 'already tracked': %s", stderr)
	}
}

func TestPush_BarePushFollowsManifest(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/data") // destination folder must exist to upload into it
	env.writeFile("data/report.csv", "report data")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local = "data/report.csv"
remote = "/data/report.csv"
version = 0
md5 = ""
`)

	// Bare push writes remote bytes, so it now requires an explicit confirmation
	// bypass in non-interactive use (--yes for a safe new-file push).
	_, stderr, code := env.run("push", "--yes", "--quiet")
	if code != 0 {
		t.Fatalf("bare push exit %d; stderr=%s", code, stderr)
	}
	uploads := env.srv.Uploads()
	if len(uploads) == 0 {
		t.Fatal("expected upload from bare push")
	}
	if uploads[0].Path != "/data/report.csv" {
		t.Errorf("upload path = %q, want /data/report.csv", uploads[0].Path)
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "version = 1") {
		t.Errorf("expected version = 1 after bare push:\n%s", toml)
	}
}

func TestPush_BarePushNoProjectID(t *testing.T) {
	env := newTestEnv(t)
	env.writeFile(".gosf/gosf.toml", "")

	_, stderr, code := env.run("push")
	if code == 0 {
		t.Error("expected error for bare push with no project configured")
	}
	if !strings.Contains(stderr, "gosf init") {
		t.Errorf("error should mention 'gosf init': %s", stderr)
	}
}

// Bare push selects the entries that hold local work the remote does not have.
// A file already byte-identical to the remote carries none, so it is not
// re-uploaded — selection is by state, never by a field on the entry (#81).
func TestPush_BareSkipsEntriesWithNothingToPublish(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/data")
	f := env.srv.AddFile("abc12", "/data/input.csv", []byte("reference data"))
	env.writeFile("data/input.csv", "reference data")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local = "data/input.csv"
remote = "/data/input.csv"
version = 1
md5 = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("push", "--quiet")
	if code != 0 {
		t.Fatalf("bare push exit %d; stderr=%s", code, stderr)
	}
	if uploads := env.srv.Uploads(); len(uploads) != 0 {
		t.Errorf("an unchanged file must not be re-uploaded; got %d uploads", len(uploads))
	}
}

// The counterpart: a locally modified file is published by bare push whatever
// the entry once declared. Before #81 a direction=pull entry refused this.
func TestPush_BarePublishesLocallyModifiedEntry(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/data")
	f := env.srv.AddFile("abc12", "/data/notes.csv", []byte("original"))
	env.writeFile("data/notes.csv", "edited locally")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "data/notes.csv"
remote    = "/data/notes.csv"
direction = "pull"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("push", "--yes", "--quiet")
	if code != 0 {
		t.Fatalf("bare push exit %d; stderr=%s", code, stderr)
	}
	uploads := env.srv.Uploads()
	if len(uploads) == 0 {
		t.Fatal("expected the modified file to be published")
	}
	if string(uploads[0].Content) != "edited locally" {
		t.Errorf("upload content = %q", uploads[0].Content)
	}
}

// ---- Sync ----

// AHEAD_OF_MANIFEST is genuinely ambiguous — the same difference means
// "publish this" for a generated output and "throw this away" for an edited
// input — so sync reports it, transfers nothing, and exits non-zero. `gosf push`
// and `gosf pull --force` are the two ways to say which one you meant (#81).
func TestSync_AheadIsReportedNotPushed(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data.csv", []byte("original"))

	env.writeFile("data.csv", "modified content")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "data.csv"
remote    = "/data.csv"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("sync", "--no-check-remote")
	if code == 0 {
		t.Fatal("sync should exit non-zero when an entry is only reported")
	}
	if len(env.srv.Uploads()) != 0 {
		t.Error("sync must not publish a locally modified file on its own")
	}
	if !strings.Contains(stderr, "locally modified") {
		t.Errorf("sync should say what it found; stderr=%s", stderr)
	}
	if toml := env.readFile(".gosf/gosf.toml"); !strings.Contains(toml, "version   = 1") {
		t.Errorf("the pin must be left alone:\n%s", toml)
	}

	// `gosf push` is the verb that means "publish it".
	if _, pstderr, pcode := env.run("push", "--yes", "--quiet"); pcode != 0 {
		t.Fatalf("push exit %d; stderr=%s", pcode, pstderr)
	}
	uploads := env.srv.Uploads()
	if len(uploads) == 0 {
		t.Fatal("expected push to upload the modified file")
	}
	if string(uploads[0].Content) != "modified content" {
		t.Errorf("upload content = %q", uploads[0].Content)
	}
	if toml := env.readFile(".gosf/gosf.toml"); !strings.Contains(toml, "version = 2") {
		t.Errorf("expected version = 2 after push:\n%s", toml)
	}
}

// The other half: `gosf sync --force` discards the local edit and restores the
// tracked version instead.
func TestSync_ForceDiscardsLocalModification(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data.csv", []byte("original"))

	env.writeFile("data.csv", "modified content")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "data.csv"
remote    = "/data.csv"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("sync", "--force", "--quiet")
	if code != 0 {
		t.Fatalf("sync --force exit %d; stderr=%s", code, stderr)
	}
	if got := env.readFile("data.csv"); got != "original" {
		t.Errorf("--force should restore the remote version, got %q", got)
	}
	if len(env.srv.Uploads()) != 0 {
		t.Error("--force restores; it must not upload")
	}
}

func TestSync_PullsMissingFile(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data.csv", []byte("remote content"))

	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "data.csv"
remote    = "/data.csv"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("sync", "--quiet")
	if code != 0 {
		t.Fatalf("sync exit %d; stderr=%s", code, stderr)
	}
	if !env.fileExists("data.csv") {
		t.Fatal("data.csv not downloaded")
	}
	if got := env.readFile("data.csv"); got != "remote content" {
		t.Errorf("content = %q", got)
	}
}

func TestSync_PullsAndPushes(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/pull-me.csv", []byte("remote content"))

	// NOT_PUSHED with local content → sync uploads it.
	env.writeFile("push-me.csv", "modified locally")
	// MISSING (file doesn't exist locally) → sync downloads it.
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "push-me.csv"
remote    = "/push-me.csv"
version   = 0
md5       = ""

[[files]]
local     = "pull-me.csv"
remote    = "/pull-me.csv"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("sync", "--quiet", "--no-check-remote")
	if code != 0 {
		t.Fatalf("sync exit %d; stderr=%s", code, stderr)
	}

	// push happened
	if uploads := env.srv.Uploads(); len(uploads) == 0 {
		t.Error("expected push upload, got none")
	} else if string(uploads[0].Content) != "modified locally" {
		t.Errorf("upload content = %q", uploads[0].Content)
	}

	// pull happened
	if !env.fileExists("pull-me.csv") {
		t.Error("pull-me.csv not downloaded")
	}
	if got := env.readFile("pull-me.csv"); got != "remote content" {
		t.Errorf("pull-me.csv content = %q", got)
	}
}

func TestSync_NoProjectID(t *testing.T) {
	env := newTestEnv(t)
	env.writeFile(".gosf/gosf.toml", `[project]
id = ""
`)

	_, stderr, code := env.run("sync")
	if code == 0 {
		t.Fatal("expected non-zero exit when no project id configured")
	}
	if !strings.Contains(stderr, "gosf init") {
		t.Errorf("expected 'gosf init' in stderr; got %q", stderr)
	}
}

func TestSync_DryRun(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data.csv", []byte("original"))

	_ = f
	env.writeFile("data.csv", "changed")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local     = "data.csv"
remote    = "/data.csv"
version   = 0
md5       = ""
`)

	stdout, stderr, code := env.run("sync", "--dry-run", "--no-check-remote")
	if code != 0 {
		t.Fatalf("sync --dry-run exit %d", code)
	}
	if len(env.srv.Uploads()) != 0 {
		t.Error("dry-run should not upload")
	}
	if stdout != "" {
		t.Errorf("text-mode dry-run should print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "dry-run") {
		t.Errorf("expected dry-run activity on stderr; got %q", stderr)
	}
}

func TestSync_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/report.csv", []byte("v1"))

	env.writeFile("report.csv", "v1") // in sync
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "report.csv"
remote    = "/report.csv"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	stdout, _, code := env.run("sync", "--output=json", "--no-check-remote")
	if code != 0 {
		t.Fatalf("sync exit %d; stdout=%s", code, stdout)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0]["state"] != "IN_SYNC" {
		t.Errorf("state = %v, want IN_SYNC", items[0]["state"])
	}
}

// ---- Status ----

func TestStatus_JSON_States(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/synced.csv", []byte("synced"))

	env.writeFile("synced.csv", "synced") // matches pinned MD5 → IN_SYNC
	// missing.csv does not exist locally → MISSING

	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "synced.csv"
remote    = "/synced.csv"
version   = 1
md5       = "%s"

[[files]]
local     = "missing.csv"
remote    = "/missing.csv"
version   = 1
md5       = "abc123def456"
`, f.VersionMD5(1)))

	stdout, _, code := env.run("status", "--output=json", "--no-check-remote")
	if code != 1 {
		t.Fatalf("status exit %d, want 1 (not all in sync); stdout=%s", code, stdout)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	states := map[string]string{}
	for _, item := range items {
		states[item["path"].(string)] = item["state"].(string)
	}
	if states["synced.csv"] != "IN_SYNC" {
		t.Errorf("synced.csv state = %q, want IN_SYNC", states["synced.csv"])
	}
	if states["missing.csv"] != "MISSING" {
		t.Errorf("missing.csv state = %q, want MISSING", states["missing.csv"])
	}
}

func TestStatus_ExitCode(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/ok.csv", []byte("ok"))

	env.writeFile("ok.csv", "ok")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "ok.csv"
remote    = "/ok.csv"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	_, _, code := env.run("status", "--no-check-remote")
	if code != 0 {
		t.Errorf("status exit %d, want 0 when all in sync", code)
	}
}

// ---- Add ----

func TestAdd_FetchesRemoteVersion(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data/counts.h5", []byte("h5 data"))

	_, stderr, code := env.run("add", "data/counts.h5", "abc12:/data/counts.h5")
	if code != 0 {
		t.Fatalf("add exit %d; stderr=%s", code, stderr)
	}

	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "version = 1") {
		t.Errorf("expected version = 1 in gosf.toml:\n%s", toml)
	}
	if !strings.Contains(toml, f.VersionMD5(1)) {
		t.Errorf("expected md5 %q in gosf.toml:\n%s", f.VersionMD5(1), toml)
	}
	if strings.Contains(toml, "direction") {
		t.Errorf("direction is no longer written to the manifest:\n%s", toml)
	}
}

func TestAdd_NoRemoteFile(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	// No file on OSF — add should still work with version=0.

	_, stderr, code := env.run("add", "local/new.csv", "abc12:/data/new.csv")
	if code != 0 {
		t.Fatalf("add exit %d; stderr=%s", code, stderr)
	}

	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "version = 0") {
		t.Errorf("expected version = 0 in gosf.toml:\n%s", toml)
	}
}

func TestAdd_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")

	stdout, _, code := env.run("add", "model.pkl", "abc12:/results/model.pkl", "--output=json")
	if code != 0 {
		t.Fatalf("add exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	entries, ok := result["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("expected non-empty entries array, got: %v", result["entries"])
	}
	entry := entries[0].(map[string]any)
	if entry["local"] != "model.pkl" {
		t.Errorf("local = %v", entry["local"])
	}
	if result["manifest_created"] != true {
		t.Errorf("manifest_created = %v", result["manifest_created"])
	}
}

// ---- Init ----

func TestInit_CreatesGOSFToml(t *testing.T) {
	env := newTestEnv(t)
	_, stderr, code := env.run("init", "abc12")
	if code != 0 {
		t.Fatalf("init exit %d; stderr=%s", code, stderr)
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "abc12") {
		t.Errorf("expected abc12 in gosf.toml:\n%s", toml)
	}
}

func TestInit_UpdatesProject(t *testing.T) {
	env := newTestEnv(t)
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"old12\"\n")
	_, stderr, code := env.run("init", "new99")
	if code != 0 {
		t.Fatalf("init exit %d; stderr=%s", code, stderr)
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "new99") {
		t.Errorf("expected new99 in gosf.toml:\n%s", toml)
	}
	if strings.Contains(toml, "old12") {
		t.Errorf("old project id should be gone:\n%s", toml)
	}
}

func TestInit_JSON(t *testing.T) {
	env := newTestEnv(t)
	stdout, _, code := env.run("init", "abc12", "--output=json")
	if code != 0 {
		t.Fatalf("init exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if result["project"] != "abc12" {
		t.Errorf("project = %v", result["project"])
	}
	if result["created"] != true {
		t.Errorf("created = %v", result["created"])
	}
}

// ---- Add (new scp-style behaviour) ----

func TestAdd_NoDestMirrorsPath(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")

	_, stderr, code := env.run("add", "data/file.txt")
	if code != 0 {
		t.Fatalf("add exit %d; stderr=%s", code, stderr)
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "/data/file.txt") {
		t.Errorf("expected /data/file.txt in gosf.toml:\n%s", toml)
	}
	if strings.Contains(toml, "direction") {
		t.Errorf("direction is no longer written to the manifest:\n%s", toml)
	}
}

func TestAdd_DestDirectory(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")

	_, stderr, code := env.run("add", "local/file.txt", "abc12:/results/")
	if code != 0 {
		t.Fatalf("add exit %d; stderr=%s", code, stderr)
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "/results/file.txt") {
		t.Errorf("expected /results/file.txt in gosf.toml:\n%s", toml)
	}
}

func TestAdd_DirectoryRecursion(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")
	env.writeFile("data/dir/file1.txt", "content")
	env.writeFile("data/dir/sub/file2.txt", "content")

	// No trailing slash: dir name preserved in remote path.
	_, stderr, code := env.run("add", "data/dir", "abc12:/results/")
	if code != 0 {
		t.Fatalf("add exit %d; stderr=%s", code, stderr)
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "/results/dir/file1.txt") {
		t.Errorf("expected /results/dir/file1.txt:\n%s", toml)
	}
	if !strings.Contains(toml, "/results/dir/sub/file2.txt") {
		t.Errorf("expected /results/dir/sub/file2.txt:\n%s", toml)
	}
}

func TestAdd_DirTrailingSlash(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")
	env.writeFile("data/dir/file.txt", "content")

	// Trailing slash: dir name stripped, contents go directly under dest.
	_, stderr, code := env.run("add", "data/dir/", "abc12:/results/")
	if code != 0 {
		t.Fatalf("add exit %d; stderr=%s", code, stderr)
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "/results/file.txt") {
		t.Errorf("expected /results/file.txt (dir name stripped):\n%s", toml)
	}
}

func TestAdd_DirectionFlagRemoved(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")

	_, _, code := env.run("add", "file.txt", "abc12:/file.txt", "--direction=pull")
	if code == 0 {
		t.Error("expected non-zero exit: --direction flag should no longer exist")
	}
}

func TestAdd_AlreadyTracked(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local = "data/file.txt"
remote = "/data/file.txt"
version = 0
md5 = ""
`)
	_, stderr, code := env.run("add", "data/file.txt", "abc12:/data/file.txt")
	if code == 0 {
		t.Error("expected error when adding already-tracked file")
	}
	if !strings.Contains(stderr, "already") {
		t.Errorf("stderr should mention 'already': %s", stderr)
	}
}

// ---- Ls ----

func TestLs_Text(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/results.csv", []byte("data"))
	env.srv.AddFile("abc12", "/report.pdf", []byte("pdf content"))

	stdout, stderr, code := env.run("ls", "abc12")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "results.csv") {
		t.Errorf("expected results.csv in output; got %q", stdout)
	}
	if !strings.Contains(stdout, "report.pdf") {
		t.Errorf("expected report.pdf in output; got %q", stdout)
	}
}

func TestLs_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("h5"))

	stdout, _, code := env.run("ls", "abc12:/data", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	attrs, _ := items[0]["attributes"].(map[string]any)
	if attrs["name"] != "counts.h5" {
		t.Errorf("name = %v, want counts.h5", attrs["name"])
	}
}

func TestLs_Empty(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	// No files — root listing should report empty.

	_, stderr, code := env.run("ls", "abc12")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "no files") {
		t.Errorf("expected 'no files' message on stderr; got stderr=%q", stderr)
	}
}

func TestLs_JSON_Empty(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")

	stdout, _, code := env.run("ls", "abc12", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var items []any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if len(items) != 0 {
		t.Errorf("expected empty array [], got %v", items)
	}
}

// ---- Rm ----

func TestRm_DryRun(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/old.csv", []byte("stale"))

	stdout, stderr, code := env.run("rm", "abc12:/old.csv", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	if len(env.srv.Deletes()) != 0 {
		t.Error("dry-run should not delete anything")
	}
	if stdout != "" {
		t.Errorf("text-mode dry-run should print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "dry-run") {
		t.Errorf("expected dry-run in activity on stderr; got %q", stderr)
	}
}

func TestRm_WithYes(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/scratch.csv", []byte("temp"))

	stdout, stderr, code := env.run("rm", "abc12:/scratch.csv", "--yes")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s stdout=%s", code, stderr, stdout)
	}
	deletes := env.srv.Deletes()
	if len(deletes) != 1 {
		t.Fatalf("deletes = %d, want 1", len(deletes))
	}
	if deletes[0] != f.ID {
		t.Errorf("deleted file ID = %q, want %q", deletes[0], f.ID)
	}
}

func TestRm_JSON_RequiresYes(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data.csv", []byte("x"))

	_, stderr, code := env.run("rm", "abc12:/data.csv", "--output=json")
	if code == 0 {
		t.Fatal("expected non-zero exit when --yes omitted in JSON mode")
	}
	if !strings.Contains(stderr, "yes") {
		t.Errorf("expected mention of --yes in error; got %q", stderr)
	}
}

func TestRm_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data.csv", []byte("content"))

	stdout, _, code := env.run("rm", "abc12:/data.csv", "--yes", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if result["node"] != "abc12" {
		t.Errorf("node = %v", result["node"])
	}
	if result["path"] != "/data.csv" {
		t.Errorf("path = %v", result["path"])
	}
	if result["kind"] != "file" {
		t.Errorf("kind = %v", result["kind"])
	}
	if result["dry_run"] != false {
		t.Errorf("dry_run = %v", result["dry_run"])
	}
}

func TestRm_JSON_DryRun(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/tmp.csv", []byte("x"))

	stdout, _, code := env.run("rm", "abc12:/tmp.csv", "--dry-run", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if result["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", result["dry_run"])
	}
	if len(env.srv.Deletes()) != 0 {
		t.Error("dry-run --output=json should not delete")
	}
}

// ---- Versions ----

func TestVersions_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data.csv", []byte("v1"))

	stdout, _, code := env.run("versions", "abc12:/data.csv", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	versions, ok := result["versions"].([]any)
	if !ok || len(versions) != 1 {
		t.Fatalf("versions = %v", result["versions"])
	}
	v := versions[0].(map[string]any)
	if v["version"] != float64(1) {
		t.Errorf("version = %v, want 1", v["version"])
	}
	if v["contributor"] != "test@example.com" {
		t.Errorf("contributor = %v, want test@example.com", v["contributor"])
	}
}

func TestVersions_Text(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/report.csv", []byte("content here"))

	stdout, stderr, code := env.run("versions", "abc12:/report.csv")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "VERSION") {
		t.Errorf("expected VERSION header; got %q", stdout)
	}
	if !strings.Contains(stdout, "1") {
		t.Errorf("expected version 1 in output; got %q", stdout)
	}
}

func TestVersions_FolderError(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	// Create a folder by adding a file inside it.
	env.srv.AddFile("abc12", "/mydir/file.csv", []byte("x"))

	_, stderr, code := env.run("versions", "abc12:/mydir")
	if code == 0 {
		t.Fatal("expected non-zero exit for folder target")
	}
	if !strings.Contains(stderr, "folder") {
		t.Errorf("expected 'folder' in error; got %q", stderr)
	}
}

// ---- Info ----

func TestInfo_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "My Research Project")

	stdout, _, code := env.run("info", "abc12", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if result["id"] != "abc12" {
		t.Errorf("id = %v, want abc12", result["id"])
	}
	attrs, _ := result["attributes"].(map[string]any)
	if attrs["title"] != "My Research Project" {
		t.Errorf("title = %v", attrs["title"])
	}
}

func TestInfo_Text(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("prj99", "Neuroscience Study")

	stdout, stderr, code := env.run("info", "prj99")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Neuroscience Study") {
		t.Errorf("expected title in output; got %q", stdout)
	}
	if !strings.Contains(stdout, "prj99") {
		t.Errorf("expected GUID in output; got %q", stdout)
	}
}

func TestInfo_NotFound(t *testing.T) {
	env := newTestEnv(t)
	// No projects registered — any lookup should 404.

	_, stderr, code := env.run("info", "xxxxx")
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown project")
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("expected 'not found' in error; got %q", stderr)
	}
}

// ---- Projects ----

func TestProjects_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("aaa11", "Project Alpha")
	env.srv.AddProject("bbb22", "Project Beta")

	stdout, _, code := env.run("projects", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(stdout), &nodes); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	ids := map[string]bool{}
	for _, n := range nodes {
		ids[n["id"].(string)] = true
	}
	if !ids["aaa11"] || !ids["bbb22"] {
		t.Errorf("expected both project IDs; got %v", ids)
	}
}

func TestProjects_Text(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("ccc33", "The Study")

	stdout, stderr, code := env.run("projects")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "The Study") {
		t.Errorf("expected project title; got %q", stdout)
	}
	if !strings.Contains(stdout, "ccc33") {
		t.Errorf("expected project GUID; got %q", stdout)
	}
}

func TestProjects_NoToken(t *testing.T) {
	env := newTestEnv(t)

	// Override to remove the token.
	cmd := exec.Command(binaryPath, "projects")
	cmd.Dir = env.dir
	cmd.Env = append(os.Environ(),
		"GOSF_API_BASE="+env.srv.URL()+"/v2",
		"GOSF_FILES_BASE="+env.srv.URL(),
		"OSF_TOKEN=",
		"HOME="+env.dir,
		"XDG_CONFIG_HOME="+filepath.Join(env.dir, ".config"),
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	if cmd.ProcessState.ExitCode() == 0 {
		t.Fatal("expected non-zero exit without token")
	}
	if !strings.Contains(errBuf.String(), "auth") {
		t.Errorf("expected auth hint; got %q", errBuf.String())
	}
}

// ---- Open ----

func TestOpen_JSON_Root(t *testing.T) {
	env := newTestEnv(t)

	stdout, _, code := env.run("open", "abc12", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	want := "https://osf.io/abc12/"
	if result["url"] != want {
		t.Errorf("url = %v, want %q", result["url"], want)
	}
}

func TestOpen_JSON_File(t *testing.T) {
	env := newTestEnv(t)

	stdout, _, code := env.run("open", "abc12:/data/results.csv", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	want := "https://osf.io/abc12/files/osfstorage/data/results.csv"
	if result["url"] != want {
		t.Errorf("url = %v, want %q", result["url"], want)
	}
}

// ---- Mkdir ----

func TestMkdir_Basic(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/results") // parent must exist to create a subfolder

	_, stderr, code := env.run("mkdir", "abc12:/results/2026")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	flds := env.srv.Folders()
	if len(flds) == 0 {
		t.Fatal("expected a folder to be created")
	}
	if flds[0] != "/results/2026" {
		t.Errorf("folder path = %q, want /results/2026", flds[0])
	}
}

func TestMkdir_RootLevel(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")

	_, stderr, code := env.run("mkdir", "abc12:/toplevel")
	if code != 0 {
		t.Fatalf("root-level mkdir exit %d; stderr=%s", code, stderr)
	}
	flds := env.srv.Folders()
	if len(flds) == 0 || flds[0] != "/toplevel" {
		t.Errorf("folders = %v, want [/toplevel]", flds)
	}
}

func TestMkdir_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/data") // parent must exist to create a subfolder

	stdout, _, code := env.run("mkdir", "abc12:/data/raw", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if result["path"] != "/data/raw" {
		t.Errorf("path = %v, want /data/raw", result["path"])
	}
	if result["created"] != true {
		t.Errorf("created = %v, want true", result["created"])
	}
}

func TestMkdir_DryRun(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")

	_, _, code := env.run("mkdir", "abc12:/dry/folder", "--dry-run")
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	if len(env.srv.Folders()) != 0 {
		t.Error("dry-run should not create folder")
	}
}

// ---- Set ----

func TestSet_Description(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "My Project")

	_, stderr, code := env.run("set", "abc12", "--description", "Updated desc")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	patches := env.srv.NodePatches()
	if len(patches) == 0 {
		t.Fatal("expected a PATCH request")
	}
	if patches[0].Attrs["description"] != "Updated desc" {
		t.Errorf("description = %v", patches[0].Attrs["description"])
	}
}

func TestSet_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "My Project")

	stdout, _, code := env.run("set", "abc12", "--title", "New Title", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if result["id"] != "abc12" {
		t.Errorf("id = %v, want abc12", result["id"])
	}
}

func TestSet_NoFlags(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "My Project")

	_, _, code := env.run("set", "abc12")
	if code == 0 {
		t.Fatal("expected non-zero exit when no flags given")
	}
}

func TestSet_MultipleFields(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "My Project")

	_, stderr, code := env.run("set", "abc12",
		"--title", "Updated",
		"--category", "analysis",
		"--tags", "processed,qc-passed",
	)
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	patches := env.srv.NodePatches()
	if len(patches) == 0 {
		t.Fatal("expected a PATCH request")
	}
	attrs := patches[0].Attrs
	if attrs["title"] != "Updated" {
		t.Errorf("title = %v", attrs["title"])
	}
	if attrs["category"] != "analysis" {
		t.Errorf("category = %v", attrs["category"])
	}
	tags, _ := attrs["tags"].([]any)
	if len(tags) != 2 {
		t.Errorf("tags = %v, want 2 elements", tags)
	}
}

// ---- Mv ----

func TestMv_Rename(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("data"))

	_, stderr, code := env.run("mv", "abc12:/data/counts.h5", "abc12:/data/counts_v2.h5")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	moves := env.srv.Moves()
	if len(moves) == 0 {
		t.Fatal("expected a move action")
	}
	if moves[0].Action != "rename" {
		t.Errorf("action = %q, want rename", moves[0].Action)
	}
	if moves[0].NewName != "counts_v2.h5" {
		t.Errorf("rename = %q, want counts_v2.h5", moves[0].NewName)
	}
}

func TestMv_Move(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("data"))

	_, stderr, code := env.run("mv", "abc12:/data/counts.h5", "abc12:/processed/counts.h5")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	moves := env.srv.Moves()
	if len(moves) == 0 {
		t.Fatal("expected a move action")
	}
	if moves[0].Action != "move" {
		t.Errorf("action = %q, want move", moves[0].Action)
	}
	if moves[0].DestPath != "/processed" {
		t.Errorf("path = %q, want /processed", moves[0].DestPath)
	}
}

func TestMv_DryRun(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("data"))

	_, _, code := env.run("mv", "abc12:/data/counts.h5", "abc12:/processed/counts.h5", "--dry-run")
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	if len(env.srv.Moves()) != 0 {
		t.Error("dry-run should not move any files")
	}
}

func TestMv_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("data"))

	stdout, _, code := env.run("mv", "abc12:/data/counts.h5", "abc12:/processed/counts.h5", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if result["src"] == nil || result["dest"] == nil {
		t.Errorf("expected src and dest in result: %v", result)
	}
}

// ---- Cp ----

func TestCp_Basic(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("data"))

	_, stderr, code := env.run("cp", "abc12:/data/counts.h5", "abc12:/backup/counts.h5")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	moves := env.srv.Moves()
	if len(moves) == 0 {
		t.Fatal("expected a copy action")
	}
	if moves[0].Action != "copy" {
		t.Errorf("action = %q, want copy", moves[0].Action)
	}
}

func TestCp_DryRun(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("data"))

	_, _, code := env.run("cp", "abc12:/data/counts.h5", "abc12:/backup/counts.h5", "--dry-run")
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	if len(env.srv.Moves()) != 0 {
		t.Error("dry-run should not copy any files")
	}
}

func TestCp_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data/counts.h5", []byte("data"))

	stdout, _, code := env.run("cp", "abc12:/data/counts.h5", "abc12:/backup/counts.h5", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stdout=%s", code, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if result["src"] == nil || result["dest"] == nil {
		t.Errorf("expected src and dest in result: %v", result)
	}
}

// ---- Epic #38: state-based sync safety ----

// TestPull_IdempotentWhenLocalIdentical is the keystone: pulling files that are
// already present locally and byte-identical to the remote performs no download,
// records a pull-pinned manifest entry, and leaves status reporting IN_SYNC.
func TestPull_IdempotentWhenLocalIdentical(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("z2qm3", "Clade Study")
	a := "gene,count\nA,5\n"
	b := "gene,count\nB,7\n"
	env.srv.AddFile("z2qm3", "/clade/a.csv", []byte(a))
	env.srv.AddFile("z2qm3", "/clade/b.csv", []byte(b))
	// Pre-place identical local copies at the destination.
	env.writeFile("ml/clade/a.csv", a)
	env.writeFile("ml/clade/b.csv", b)

	_, stderr, code := env.run("pull", "z2qm3:/clade/", "ml/clade/")
	if code != 0 {
		t.Fatalf("pull exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "identical") {
		t.Errorf("expected an identical-skip note, stderr=%q", stderr)
	}
	if env.readFile("ml/clade/a.csv") != a {
		t.Error("a.csv content changed unexpectedly")
	}
	mani := env.readFile(".gosf/gosf.toml")
	for _, want := range []string{"ml/clade/a.csv", "ml/clade/b.csv"} {
		if !strings.Contains(mani, want) {
			t.Errorf("manifest missing %q:\n%s", want, mani)
		}
	}
	if strings.Contains(mani, "version = 0") {
		t.Errorf("entries should be pinned to a non-zero version:\n%s", mani)
	}
	out2, err2, code2 := env.run("status")
	if code2 != 0 {
		t.Fatalf("status exit %d, want 0 (all in sync); stdout=%s stderr=%s", code2, out2, err2)
	}
}

// TestStatus_UnpinnedContentMatchIsPinOnly verifies status content-compares an
// unpinned (version=0) entry against the remote instead of reporting NOT_PUSHED.
func TestStatus_UnpinnedContentMatchIsPinOnly(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/inputs/x.csv", []byte("payload"))
	env.writeFile("inputs/x.csv", "payload")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local     = "inputs/x.csv"
remote    = "/inputs/x.csv"
version   = 0
md5       = ""
`)

	stdout, _, code := env.run("status", "--output=json")
	if code != 1 {
		t.Fatalf("status exit %d, want 1 (pin needed); stdout=%s", code, stdout)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if len(items) != 1 || items[0]["state"] != "PIN_ONLY" {
		t.Errorf("state = %v, want PIN_ONLY", items)
	}
}

// TestSync_PinsUnpinnedIdentical verifies sync records the pin for a byte-identical
// unpinned entry without uploading anything.
func TestSync_PinsUnpinnedIdentical(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/inputs/x.csv", []byte("payload"))
	env.writeFile("inputs/x.csv", "payload")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local     = "inputs/x.csv"
remote    = "/inputs/x.csv"
version   = 0
md5       = ""
`)

	_, stderr, code := env.run("sync")
	if code != 0 {
		t.Fatalf("sync exit %d; stderr=%s", code, stderr)
	}
	if len(env.srv.Uploads()) != 0 {
		t.Errorf("sync uploaded %d files; expected 0 (content identical)", len(env.srv.Uploads()))
	}
	mani := env.readFile(".gosf/gosf.toml")
	if strings.Contains(mani, "version = 0") {
		t.Errorf("entry should be pinned after sync:\n%s", mani)
	}
}

// TestSync_RemoteNewerFastForwardPull advances a pull entry to the latest remote
// version when only the remote moved.
func TestSync_RemoteNewerFastForwardPull(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data.csv", []byte("v1-content"))
	env.srv.AddVersion("abc12", "/data.csv", []byte("v2-content"))
	env.writeFile("data.csv", "v1-content")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "data.csv"
remote    = "/data.csv"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("sync")
	if code != 0 {
		t.Fatalf("sync exit %d; stderr=%s", code, stderr)
	}
	if got := env.readFile("data.csv"); got != "v2-content" {
		t.Errorf("expected fast-forward to v2 content, got %q", got)
	}
	mani := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(mani, "version = 2") {
		t.Errorf("expected re-pin to v2:\n%s", mani)
	}
}

// TestSync_DivergenceFailsHard refuses to transfer when both sides changed since
// the baseline, and names both --resolve options.
func TestSync_DivergenceFailsHard(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/species.csv", []byte("baseline"))
	env.srv.AddVersion("abc12", "/species.csv", []byte("remote-edit"))
	env.writeFile("species.csv", "local-edit")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "species.csv"
remote    = "/species.csv"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("sync")
	if code == 0 {
		t.Fatalf("sync should fail hard on divergence; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "divergence") || !strings.Contains(stderr, "--resolve=theirs") {
		t.Errorf("expected divergence diagnostic with resolve options; stderr=%s", stderr)
	}
	if len(env.srv.Uploads()) != 0 {
		t.Error("divergence must not upload anything")
	}
	if env.readFile("species.csv") != "local-edit" {
		t.Error("local file must be left untouched on divergence")
	}
}

// TestPush_JSONRequiresForce verifies a push writing remote bytes in JSON mode
// refuses without --force (no prompt possible), mirroring `gosf rm`.
func TestPush_JSONRequiresForce(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile("new.csv", "brand new content")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local     = "new.csv"
remote    = "/new.csv"
version   = 0
md5       = ""
`)

	_, stderr, code := env.run("push", "--output=json")
	if code == 0 {
		t.Fatalf("push --output=json without --force should fail; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("error should mention --force; stderr=%s", stderr)
	}
	if len(env.srv.Uploads()) != 0 {
		t.Error("no upload should occur when the confirmation gate blocks")
	}

	_, stderr2, code2 := env.run("push", "--output=json", "--force")
	if code2 != 0 {
		t.Fatalf("push --force exit %d; stderr=%s", code2, stderr2)
	}
	if len(env.srv.Uploads()) != 1 {
		t.Errorf("expected 1 upload with --force, got %d", len(env.srv.Uploads()))
	}
}

// ---- Auth ----

func TestAuth_StatusUnauthenticated(t *testing.T) {
	env := newTestEnv(t)
	stdout, _, code := env.runClean("", "auth", "status")
	if code != 0 {
		t.Fatalf("auth status (unauth) exit %d", code)
	}
	if !strings.Contains(stdout, "Not logged in") {
		t.Errorf("expected 'Not logged in', got %q", stdout)
	}
}

func TestAuth_StatusFromEnvToken(t *testing.T) {
	env := newTestEnv(t)
	// run() sets OSF_TOKEN=test-token; the fake accepts any bearer by default.
	stdout, stderr, code := env.run("auth", "status")
	if code != 0 {
		t.Fatalf("auth status exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Logged in as: Test User") {
		t.Errorf("expected logged-in identity, got %q", stdout)
	}
	if !strings.Contains(stdout, "OSF_TOKEN environment variable") {
		t.Errorf("expected token source to be the env var, got %q", stdout)
	}
}

func TestAuth_LoginStatusLogout(t *testing.T) {
	env := newTestEnv(t)

	// login: pipe the token on stdin, store to the token file (no keychain).
	out, stderr, code := env.runClean("secret-token\n", "auth", "login", "--no-keychain")
	if code != 0 {
		t.Fatalf("auth login exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(out, "Logged in as Test User") {
		t.Errorf("expected login confirmation, got %q", out)
	}

	// status: now reads the token file.
	out, _, code = env.runClean("", "auth", "status")
	if code != 0 || !strings.Contains(out, "Logged in as: Test User") {
		t.Fatalf("auth status after login: exit %d, out=%q", code, out)
	}
	if !strings.Contains(out, "token file") {
		t.Errorf("expected token source 'token file', got %q", out)
	}

	// logout: removes the stored token.
	out, stderr, code = env.runClean("", "auth", "logout")
	if code != 0 {
		t.Fatalf("auth logout exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(out, "Logged out") {
		t.Errorf("expected 'Logged out', got %q", out)
	}

	// status: back to unauthenticated.
	out, _, _ = env.runClean("", "auth", "status")
	if !strings.Contains(out, "Not logged in") {
		t.Errorf("expected 'Not logged in' after logout, got %q", out)
	}
}

func TestAuth_LoginInvalidToken(t *testing.T) {
	env := newTestEnv(t)
	env.srv.SetValidToken("good-token") // anything else 401s

	_, stderr, code := env.runClean("wrong-token\n", "auth", "login", "--no-keychain")
	if code == 0 {
		t.Fatalf("auth login with a bad token should fail; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "invalid token") {
		t.Errorf("expected 'invalid token' error, got %q", stderr)
	}
	// A failed login must not leave a stored token behind.
	out, _, _ := env.runClean("", "auth", "status")
	if !strings.Contains(out, "Not logged in") {
		t.Errorf("failed login should store nothing, got %q", out)
	}
}

// ---- Color control ----

// TestColor_FlagControlsANSI verifies the --color flag forces ANSI on/off, and
// that auto mode (piped to a non-TTY here) stays plain — proving scripts are
// unaffected.
func TestColor_FlagControlsANSI(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/a.csv", []byte("x"))

	always, _, code := env.run("ls", "abc12", "--color=always")
	if code != 0 {
		t.Fatalf("ls --color=always exit %d", code)
	}
	if !strings.Contains(always, "\x1b[") {
		t.Errorf("--color=always should emit ANSI escapes, got %q", always)
	}

	never, _, code2 := env.run("ls", "abc12", "--color=never")
	if code2 != 0 {
		t.Fatalf("ls --color=never exit %d", code2)
	}
	if strings.Contains(never, "\x1b[") {
		t.Errorf("--color=never should emit no ANSI, got %q", never)
	}

	// Default auto mode, output captured to a buffer (not a TTY) → no color.
	auto, _, _ := env.run("ls", "abc12")
	if strings.Contains(auto, "\x1b[") {
		t.Errorf("auto mode off a TTY should emit no ANSI, got %q", auto)
	}
}

// TestColor_JSONNeverColored guards the JSON contract: even with --color=always,
// json output must be plain so it stays parseable.
func TestColor_JSONNeverColored(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/a.csv", []byte("x"))

	out, _, code := env.run("ls", "abc12", "--output=json", "--color=always")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("json output must never contain ANSI, got %q", out)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("json not parseable: %v\n%s", err, out)
	}
}

// ---- Error paths ----

func TestLs_NotFoundProject(t *testing.T) {
	env := newTestEnv(t)
	_, stderr, code := env.run("ls", "nope1")
	if code == 0 {
		t.Fatal("ls of a nonexistent project should fail")
	}
	if !strings.Contains(stderr, "404") {
		t.Errorf("expected a 404 error, got %q", stderr)
	}
}

func TestLs_ForbiddenProject_FriendlyAuthError(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("priv01", "Private")
	env.srv.SetForbidden("priv01")

	_, stderr, code := env.run("ls", "priv01")
	if code == 0 {
		t.Fatal("ls of a forbidden project should fail")
	}
	if !strings.Contains(stderr, "gosf auth login") || !strings.Contains(stderr, "403") {
		t.Errorf("expected a friendly 403 auth hint, got %q", stderr)
	}
}

func TestPull_ForbiddenProject_FriendlyAuthError(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("priv02", "Private")
	env.srv.SetForbidden("priv02")

	_, stderr, code := env.run("pull", "priv02:/x.csv", "--no-track")
	if code == 0 || !strings.Contains(stderr, "gosf auth login") {
		t.Errorf("expected a friendly 403 auth hint on pull; code=%d stderr=%q", code, stderr)
	}
}

func TestPull_PathNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test")
	_, stderr, code := env.run("pull", "abc12:/missing.csv", "--no-track")
	if code == 0 || !strings.Contains(stderr, "not found") {
		t.Errorf("expected 'not found'; code=%d stderr=%q", code, stderr)
	}
}

func TestPull_VersionOnDirectory(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test")
	env.srv.AddFile("abc12", "/data/a.csv", []byte("a"))
	env.srv.AddFile("abc12", "/data/b.csv", []byte("b"))

	_, stderr, code := env.run("pull", "abc12:/data", "out/", "--version=1", "--no-track")
	if code == 0 || !strings.Contains(stderr, "single file") {
		t.Errorf("expected --version single-file error; code=%d stderr=%q", code, stderr)
	}
}

func TestPull_VersionNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test")
	env.srv.AddFile("abc12", "/data.csv", []byte("v1"))

	_, stderr, code := env.run("pull", "abc12:/data.csv", "--version=99", "--no-track")
	if code == 0 || !strings.Contains(stderr, "99") {
		t.Errorf("expected version-99-not-found error; code=%d stderr=%q", code, stderr)
	}
}

func TestPush_ParentFolderMissing(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test")
	env.writeFile("x.csv", "data")

	_, stderr, code := env.run("push", "x.csv", "abc12:/nope/x.csv", "--no-track", "--quiet")
	if code == 0 || !strings.Contains(stderr, "not accessible") {
		t.Errorf("expected 'not accessible' for a missing parent folder; code=%d stderr=%q", code, stderr)
	}
}

func TestPush_ConflictRename(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test")
	env.srv.AddFile("abc12", "/data.csv", []byte("existing"))
	env.writeFile("data.csv", "new content")

	_, stderr, code := env.run("push", "data.csv", "abc12:/data.csv", "--conflict=rename", "--no-track", "--quiet")
	if code != 0 {
		t.Fatalf("rename push exit %d; stderr=%s", code, stderr)
	}
	out, _, _ := env.run("ls", "abc12", "--output=json")
	if !strings.Contains(out, "data_1.csv") {
		t.Errorf("expected a renamed data_1.csv to be created:\n%s", out)
	}
}

func TestPush_ConflictInvalid(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test")
	env.writeFile("x.csv", "data")
	_, stderr, code := env.run("push", "x.csv", "abc12:/x.csv", "--conflict=bogus", "--quiet")
	if code == 0 || !strings.Contains(stderr, "conflict") {
		t.Errorf("expected a --conflict validation error; code=%d stderr=%q", code, stderr)
	}
}

func TestRm_NotFound(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test")
	_, stderr, code := env.run("rm", "abc12:/missing.csv", "--yes", "--quiet")
	if code == 0 || !strings.Contains(stderr, "not found") {
		t.Errorf("expected 'not found' on rm of a missing path; code=%d stderr=%q", code, stderr)
	}
}

func TestStatus_MalformedManifest(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test")
	// Two entries claiming the same local path → load must fail.
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local  = "x.csv"
remote = "/x.csv"
version = 0

[[files]]
local  = "x.csv"
remote = "/other.csv"
version = 0
`)
	_, stderr, code := env.run("status")
	if code == 0 || !strings.Contains(stderr, "duplicate") {
		t.Errorf("expected a duplicate-local-path validation error; code=%d stderr=%q", code, stderr)
	}
}

// A manifest written by gosf <= 1.9 still carries `direction` on every entry.
// It must keep working: the key is ignored with a warning, never an error, and
// it is dropped the next time gosf writes the file (#81).
func TestStatus_LegacyDirectionManifestStillWorks(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test")
	f := env.srv.AddFile("abc12", "/x.csv", []byte("data"))
	env.writeFile("x.csv", "data")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "x.csv"
remote    = "/x.csv"
direction = "pull"
version   = 1
md5       = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("status")
	if code != 0 {
		t.Fatalf("a legacy manifest must still load and report in sync; exit %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "direction") {
		t.Errorf("loading a legacy manifest should warn about the retired key; stderr=%q", stderr)
	}
}

func TestStatus_NoManifest_SuggestsInit(t *testing.T) {
	env := newTestEnv(t)
	_, stderr, code := env.run("status")
	if code == 0 {
		t.Fatal("status without a manifest should fail")
	}
	if !strings.Contains(stderr, "gosf init") {
		t.Errorf("missing-manifest error should suggest 'gosf init', got %q", stderr)
	}
}

// ---- Onboard (guard paths only; the interactive TUI needs a real TTY) ----

func TestOnboard_RequiresTTY(t *testing.T) {
	env := newTestEnv(t)
	_, stderr, code := env.run("onboard") // harness stdin is not a TTY
	if code == 0 || !strings.Contains(stderr, "interactive") {
		t.Errorf("onboard should require an interactive terminal; code=%d stderr=%q", code, stderr)
	}
}

func TestOnboard_JSONRefused(t *testing.T) {
	env := newTestEnv(t)
	_, stderr, code := env.run("onboard", "--output=json")
	if code == 0 || !strings.Contains(stderr, "json") {
		t.Errorf("onboard --output=json should be refused; code=%d stderr=%q", code, stderr)
	}
}

// ---- Logging / verbosity (PR1: sync as the reference command) ----

// syncPushEnv sets up an unpinned entry whose local content exists so a default
// sync contacts the remote (scan phase) and then uploads.
func syncPushEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile("data.csv", "new content")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local     = "data.csv"
remote    = "/data.csv"
version   = 0
md5       = ""
`)
	return env
}

// A default sync routes all activity to stderr and leaves stdout empty (stdout
// is reserved for machine/result output), and the remote-scan phase reports
// progress so a large manifest never looks stalled.
func TestSync_ActivityGoesToStderrNotStdout(t *testing.T) {
	env := syncPushEnv(t)
	stdout, stderr, code := env.run("sync")
	if code != 0 {
		t.Fatalf("sync exit %d; stderr=%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("text-mode sync should print nothing to stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "scanned remote") {
		t.Errorf("scan phase should report progress on stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "pushed") {
		t.Errorf("transfer should be logged on stderr, got:\n%s", stderr)
	}
}

// --quiet drops activity logging to errors-only: no scan/transfer chatter.
func TestSync_QuietSuppressesActivity(t *testing.T) {
	env := syncPushEnv(t)
	stdout, stderr, code := env.run("sync", "--quiet")
	if code != 0 {
		t.Fatalf("sync exit %d; stderr=%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty under --quiet, got:\n%s", stdout)
	}
	for _, chatter := range []string{"scanned remote", "pushed"} {
		if strings.Contains(stderr, chatter) {
			t.Errorf("--quiet should suppress %q, got:\n%s", chatter, stderr)
		}
	}
	// The upload must still happen (quiet silences output, not work).
	if len(env.srv.Uploads()) == 0 {
		t.Error("expected the upload to happen even under --quiet")
	}
}

// -v surfaces per-item DEBUG detail (e.g. the upload URL / resolve steps).
func TestSync_VerboseAddsDebugDetail(t *testing.T) {
	env := syncPushEnv(t)
	_, stderrPlain, _ := env.run("sync", "--dry-run")
	if strings.Contains(stderrPlain, "DEBUG") {
		t.Errorf("default sync should not emit DEBUG lines, got:\n%s", stderrPlain)
	}
	env2 := syncPushEnv(t)
	_, stderrV, code := env2.run("sync", "-v", "--dry-run")
	if code != 0 {
		t.Fatalf("sync -v exit %d; stderr=%s", code, stderrV)
	}
	if !strings.Contains(stderrV, "DEBUG") {
		t.Errorf("-v should emit DEBUG lines, got:\n%s", stderrV)
	}
}

// --quiet and --verbose are contradictory and rejected up front.
func TestQuietVerboseConflict(t *testing.T) {
	env := newTestEnv(t)
	_, stderr, code := env.run("sync", "--quiet", "-v")
	if code == 0 {
		t.Fatal("combining --quiet and --verbose should fail")
	}
	if !strings.Contains(stderr, "quiet") || !strings.Contains(stderr, "verbose") {
		t.Errorf("error should name both flags, got:\n%s", stderr)
	}
}

// ---- PR2 sweep: stdout/stderr contract across commands ----

// A query command keeps its result on stdout and routes activity to stderr.
func TestLs_ResultOnStdoutActivityOnStderr(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data.csv", []byte("x"))

	stdout, stderr, code := env.run("ls", "abc12")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "data.csv") {
		t.Errorf("ls result (the table) must stay on stdout; got %q", stdout)
	}
	if !strings.Contains(stderr, "listing") {
		t.Errorf("ls activity should be on stderr; got %q", stderr)
	}
	if strings.Contains(stdout, "listing") {
		t.Errorf("activity must not leak onto stdout; got %q", stdout)
	}
}

// A mutation command prints its confirmation to stderr (stdout empty in text mode).
func TestMkdir_ConfirmationToStderr(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")

	stdout, stderr, code := env.run("mkdir", "abc12:/newdir")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("text-mode mkdir should print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "created") {
		t.Errorf("mkdir confirmation should be on stderr; got %q", stderr)
	}
}

// Explicit-form push routes per-file transfer activity to stderr; stdout empty.
func TestPush_ExplicitActivityToStderr(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile("data.csv", "hello")

	stdout, stderr, code := env.run("push", "data.csv", "abc12:/data.csv")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if len(env.srv.Uploads()) == 0 {
		t.Fatal("expected an upload")
	}
	if stdout != "" {
		t.Errorf("text-mode push should print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "↑") {
		t.Errorf("push activity should be logged on stderr; got %q", stderr)
	}
}

// --output=json keeps stdout pure JSON and silences activity logging by default.
func TestPush_JSONStdoutStaysPure(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile("data.csv", "hello")

	stdout, stderr, code := env.run("push", "data.csv", "abc12:/data.csv", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Errorf("json stdout should be a pure JSON object; got %q", stdout)
	}
	if strings.Contains(stderr, "↑") || strings.Contains(stderr, "INFO") {
		t.Errorf("json mode should silence activity logs by default; got stderr=%q", stderr)
	}
}

// ---- Scan speedup: skip version history when the listing settles it ----

// A status scan of files already in sync must NOT fetch per-file version
// history — the directory listing's latest MD5 + current_version is enough.
func TestStatus_SkipsVersionHistoryWhenInSync(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f1 := env.srv.AddFile("abc12", "/a.csv", []byte("aaa"))
	f2 := env.srv.AddFile("abc12", "/b.csv", []byte("bbb"))
	env.writeFile("a.csv", "aaa")
	env.writeFile("b.csv", "bbb")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local = "a.csv"
remote = "/a.csv"
version = 1
md5 = "%s"

[[files]]
local = "b.csv"
remote = "/b.csv"
version = 1
md5 = "%s"
`, f1.VersionMD5(1), f2.VersionMD5(1)))

	_, stderr, code := env.run("status")
	if code != 0 {
		t.Fatalf("status exit %d; stderr=%s", code, stderr)
	}
	if got := env.srv.VersionsRequests(); got != 0 {
		t.Errorf("in-sync files should skip version history; got %d versions requests", got)
	}
}

// When local content differs from the remote latest, the scan must fall back to
// fetching version history (to tell BEHIND from AHEAD/DIVERGED).
func TestStatus_FetchesVersionHistoryWhenLocalDiffers(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/a.csv", []byte("v1"))
	env.srv.AddVersion("abc12", "/a.csv", []byte("v2-latest"))
	// Pinned at the LATEST (v2), but local content matches the OLDER v1. Local
	// differs from both baseline and remote latest → BEHIND, which needs the
	// version history to recognize "v1" as a known older version.
	env.writeFile("a.csv", "v1")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local = "a.csv"
remote = "/a.csv"
version = 2
md5 = "%s"
`, f.VersionMD5(2)))

	stdout, stderr, code := env.run("status")
	// BEHIND → exit 1 (not in sync); that's expected, just check the scan.
	_ = code
	if !strings.Contains(stdout, "BEHIND") {
		t.Errorf("expected BEHIND state; stdout=%q stderr=%q", stdout, stderr)
	}
	if got := env.srv.VersionsRequests(); got == 0 {
		t.Error("a file differing from remote latest must fetch version history")
	}
}

// ---- #62: version=0 entry whose remote file already exists ----

// A manifest entry pinned version=0 whose remote file already exists, with
// DIFFERENT local content, must push a NEW VERSION (PUT to the file's upload
// link) rather than blindly creating (which 409s). Regression for #62.
func TestSync_UnpushedEntryRemoteExists_DiffersPushesNewVersion(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/ml")
	env.srv.AddFile("abc12", "/ml/plan.md", []byte("remote original"))

	env.writeFile("ml/plan.md", "local changed content")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local     = "ml/plan.md"
remote    = "/ml/plan.md"
version   = 0
md5       = ""
`)

	stdout, stderr, code := env.run("push", "--yes", "--quiet")
	if code != 0 {
		t.Fatalf("push should mint a new version on an existing remote, not 409; exit %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	// A new version must have been created (upload happened).
	if len(env.srv.Uploads()) == 0 {
		t.Fatal("expected a new-version upload")
	}
	// Manifest is repaired to the new remote version (v2) with the new md5.
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "version = 2") {
		t.Errorf("expected version = 2 after new-version push; got:\n%s", toml)
	}
}

// Same shape but local content already EQUALS the remote → PIN_ONLY: record the
// pin, no transfer.
func TestSync_UnpushedEntryRemoteExists_IdenticalPinsNoTransfer(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/ml")
	f := env.srv.AddFile("abc12", "/ml/plan.md", []byte("same bytes"))

	env.writeFile("ml/plan.md", "same bytes")
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local     = "ml/plan.md"
remote    = "/ml/plan.md"
version   = 0
md5       = ""
`)

	stdout, stderr, code := env.run("sync")
	if code != 0 {
		t.Fatalf("sync exit %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if len(env.srv.Uploads()) != 0 {
		t.Errorf("identical content should not upload; got %d uploads", len(env.srv.Uploads()))
	}
	toml := env.readFile(".gosf/gosf.toml")
	if !strings.Contains(toml, "version = 1") || !strings.Contains(toml, f.VersionMD5(1)) {
		t.Errorf("expected pin to remote v1 (%s); got:\n%s", f.VersionMD5(1), toml)
	}
}

// ---- Issue #81: direction removed; sync reconciles by state ----

// The bug: `sync` refused to create files that exist on the remote but not
// locally, and no flag changed it. Every entry here is MISSING with content on
// the remote — including entries still carrying each of the three legacy
// `direction` values — and a plain `gosf sync` must restore all of them.
func TestSync_RestoresMissingFilesWithNoFlags(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/results")

	type fixture struct{ local, remote, content, legacy string }
	fixtures := []fixture{
		{"results/report.pdf", "/results/report.pdf", "pdf bytes", "push"},
		{"results/concordance.tsv", "/results/concordance.tsv", "a\tb\n", "pull"},
		{"results/summary.txt", "/results/summary.txt", "summary", "both"},
	}

	toml := "[project]\nid = \"abc12\"\n"
	for _, f := range fixtures {
		file := env.srv.AddFile("abc12", f.remote, []byte(f.content))
		toml += fmt.Sprintf(`
[[files]]
local     = %q
remote    = %q
direction = %q
version   = 1
md5       = %q
`, f.local, f.remote, f.legacy, file.VersionMD5(1))
	}
	env.writeFile(".gosf/gosf.toml", toml)

	_, stderr, code := env.run("sync")
	if code != 0 {
		t.Fatalf("sync exit %d (no --force should be needed); stderr=%s", code, stderr)
	}
	for _, f := range fixtures {
		if !env.fileExists(f.local) {
			t.Errorf("%s (legacy direction=%s) was not restored", f.local, f.legacy)
			continue
		}
		if got := env.readFile(f.local); got != f.content {
			t.Errorf("%s content = %q, want %q", f.local, got, f.content)
		}
	}
	if _, _, code := env.run("status"); code != 0 {
		t.Errorf("status should report everything in sync after the restore, got %d", code)
	}
}

// An unpinned (version = 0) entry whose remote holds the only copy lands in
// MISSING too, and must be restored just the same.
func TestSync_RestoresMissingUnpinnedEntry(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data.csv", []byte("remote only"))
	env.writeFile(".gosf/gosf.toml", `[project]
id = "abc12"

[[files]]
local   = "data.csv"
remote  = "/data.csv"
version = 0
md5     = ""
`)

	_, stderr, code := env.run("sync")
	if code != 0 {
		t.Fatalf("sync exit %d; stderr=%s", code, stderr)
	}
	if got := env.readFile("data.csv"); got != "remote only" {
		t.Errorf("data.csv = %q", got)
	}
	if mani := env.readFile(".gosf/gosf.toml"); !strings.Contains(mani, "version = 1") {
		t.Errorf("the restored entry should be pinned:\n%s", mani)
	}
}

// divergedEnv builds a manifest whose single entry has diverged (local and
// remote both moved off the pinned baseline), still carrying a legacy
// `direction` value so the test proves it no longer steers --resolve.
func divergedEnv(t *testing.T, legacyDirection string) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data.csv", []byte("baseline"))
	baseline := f.VersionMD5(1)
	env.srv.AddVersion("abc12", "/data.csv", []byte("remote edit"))
	env.writeFile("data.csv", "local edit")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "data.csv"
remote    = "/data.csv"
direction = %q
version   = 1
md5       = "%s"
`, legacyDirection, baseline))
	return env
}

// --resolve is an at-the-moment choice, strictly more current than anything
// recorded on the entry, so sync must honor it as given — both ways, for every
// legacy direction. Previously `--resolve=theirs` was rejected on a
// direction=push entry and vice versa.
func TestSync_ResolveIsHonoredRegardlessOfLegacyDirection(t *testing.T) {
	for _, legacy := range []string{"push", "pull", "both"} {
		t.Run("theirs/"+legacy, func(t *testing.T) {
			env := divergedEnv(t, legacy)
			if _, _, code := env.run("sync"); code == 0 {
				t.Fatal("an unresolved divergence must fail")
			}
			_, stderr, code := env.run("sync", "--resolve=theirs", "--quiet")
			if code != 0 {
				t.Fatalf("sync --resolve=theirs exit %d; stderr=%s", code, stderr)
			}
			if got := env.readFile("data.csv"); got != "remote edit" {
				t.Errorf("--resolve=theirs should take the remote, got %q", got)
			}
			if len(env.srv.Uploads()) != 0 {
				t.Error("--resolve=theirs must not upload")
			}
		})

		t.Run("ours/"+legacy, func(t *testing.T) {
			env := divergedEnv(t, legacy)
			_, stderr, code := env.run("sync", "--resolve=ours", "--quiet")
			if code != 0 {
				t.Fatalf("sync --resolve=ours exit %d; stderr=%s", code, stderr)
			}
			uploads := env.srv.Uploads()
			if len(uploads) == 0 {
				t.Fatal("--resolve=ours should publish the local copy")
			}
			if string(uploads[0].Content) != "local edit" {
				t.Errorf("upload content = %q", uploads[0].Content)
			}
			if got := env.readFile("data.csv"); got != "local edit" {
				t.Errorf("--resolve=ours must not touch the local file, got %q", got)
			}
		})
	}
}

// A formerly-`both` entry whose remote moved ahead used to hit the push
// handler's rollback refusal, because push always won for `both`. It is a plain
// fast-forward.
func TestSync_LegacyBothEntryFastForwards(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data.csv", []byte("v1 content"))
	baseline := f.VersionMD5(1)
	env.srv.AddVersion("abc12", "/data.csv", []byte("v2 content"))
	env.writeFile("data.csv", "v1 content")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local     = "data.csv"
remote    = "/data.csv"
direction = "both"
version   = 1
md5       = "%s"
`, baseline))

	_, stderr, code := env.run("sync", "--quiet")
	if code != 0 {
		t.Fatalf("sync exit %d; stderr=%s", code, stderr)
	}
	if got := env.readFile("data.csv"); got != "v2 content" {
		t.Errorf("expected a fast-forward to v2, got %q", got)
	}
	if len(env.srv.Uploads()) != 0 {
		t.Error("a fast-forward must not push the old content back")
	}
}

// `gosf pull` over a subtree used to replace whole manifest entries, flipping
// tracked outputs to direction=pull and quietly stopping them from being
// published again. Pulling must leave later sync/push behaviour unchanged.
func TestPull_SubtreeDoesNotChangeLaterPushBehaviour(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/data")
	f := env.srv.AddFile("abc12", "/data/out.csv", []byte("published v1"))
	env.writeFile("data/out.csv", "published v1")
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local   = "data/out.csv"
remote  = "/data/out.csv"
version = 1
md5     = "%s"
`, f.VersionMD5(1)))

	if _, stderr, code := env.run("pull", "abc12:/data/", "data/", "--quiet"); code != 0 {
		t.Fatalf("pull exit %d; stderr=%s", code, stderr)
	}

	// The output is regenerated locally, then published as before.
	env.writeFile("data/out.csv", "published v2")
	_, stderr, code := env.run("push", "--yes", "--quiet")
	if code != 0 {
		t.Fatalf("push exit %d; stderr=%s", code, stderr)
	}
	uploads := env.srv.Uploads()
	if len(uploads) == 0 {
		t.Fatal("pulling the subtree must not stop the entry from being published")
	}
	if string(uploads[len(uploads)-1].Content) != "published v2" {
		t.Errorf("upload content = %q", uploads[len(uploads)-1].Content)
	}
}

// ---- pull --track-only ----

// `sync` only ever visits entries in the manifest, so remote files with no entry
// are structurally invisible. --track-only registers a remote subtree without
// transferring anything, so a large project can be adopted and reviewed before
// it is downloaded; a plain `gosf sync` then fetches it.
func TestPull_TrackOnlyRegistersWithoutDownloading(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/data")
	a := env.srv.AddFile("abc12", "/data/a.csv", []byte("alpha"))
	env.srv.AddFile("abc12", "/data/b.csv", []byte("beta"))
	env.writeFile(".gosf/gosf.toml", "[project]\nid = \"abc12\"\n")

	_, stderr, code := env.run("pull", "abc12:/data/", "data/", "--track-only")
	if code != 0 {
		t.Fatalf("pull --track-only exit %d; stderr=%s", code, stderr)
	}
	if env.fileExists("data/a.csv") || env.fileExists("data/b.csv") {
		t.Fatal("--track-only must not transfer any bytes")
	}

	mani := env.readFile(".gosf/gosf.toml")
	for _, want := range []string{"data/a.csv", "data/b.csv", "/data/a.csv", "/data/b.csv"} {
		if !strings.Contains(mani, want) {
			t.Errorf("manifest missing %q:\n%s", want, mani)
		}
	}
	if !strings.Contains(mani, a.VersionMD5(1)) {
		t.Errorf("entries should be pinned to the remote MD5:\n%s", mani)
	}
	if strings.Contains(mani, "version = 0") {
		t.Errorf("entries should be pinned to a non-zero version:\n%s", mani)
	}

	// The registered entries are MISSING, so a plain sync fetches them.
	if _, stderr, code := env.run("sync", "--quiet"); code != 0 {
		t.Fatalf("sync after --track-only exit %d; stderr=%s", code, stderr)
	}
	if got := env.readFile("data/a.csv"); got != "alpha" {
		t.Errorf("data/a.csv = %q", got)
	}
	if got := env.readFile("data/b.csv"); got != "beta" {
		t.Errorf("data/b.csv = %q", got)
	}
	if _, _, code := env.run("status"); code != 0 {
		t.Errorf("status should be in sync after the sync, got %d", code)
	}
}

func TestPull_TrackOnlyRejectsNoTrack(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFile("abc12", "/data.csv", []byte("x"))
	_, stderr, code := env.run("pull", "abc12:/data.csv", "--track-only", "--no-track")
	if code == 0 {
		t.Fatal("--track-only and --no-track are contradictory and must be rejected")
	}
	if !strings.Contains(stderr, "track-only") {
		t.Errorf("error should name the flag; stderr=%q", stderr)
	}
}

// --no-check-remote skips the classification scan, so a MISSING entry has no
// resolved download URL from pass 1. It must still be restored: the resolution
// happens lazily, for the entries that actually transfer.
func TestSync_RestoresMissingFileWithoutRemoteScan(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	f := env.srv.AddFile("abc12", "/data.csv", []byte("remote content"))
	env.writeFile(".gosf/gosf.toml", fmt.Sprintf(`[project]
id = "abc12"

[[files]]
local   = "data.csv"
remote  = "/data.csv"
version = 1
md5     = "%s"
`, f.VersionMD5(1)))

	_, stderr, code := env.run("sync", "--no-check-remote", "--quiet")
	if code != 0 {
		t.Fatalf("sync exit %d; stderr=%s", code, stderr)
	}
	if got := env.readFile("data.csv"); got != "remote content" {
		t.Errorf("data.csv = %q", got)
	}
}

// ---- Issue #86: request efficiency and rate-limit resilience ----

// OSF serves 10 items per page by default and caps at 100. gosf never asked for
// a size, so a folder of 87 files cost 9 requests instead of 1 — the dominant
// source of rate-limit pressure on a large manifest. fakeosf now paginates
// exactly like OSF, so this measures the real thing.
func TestLs_LargeFolderCostsOneRequestNotTen(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/data")
	const n = 87
	for i := 0; i < n; i++ {
		env.srv.AddFile("abc12", fmt.Sprintf("/data/f%02d.csv", i), []byte("x"))
	}

	before := env.srv.ListRequests()
	stdout, stderr, code := env.run("ls", "abc12:/data", "--output=json")
	if code != 0 {
		t.Fatalf("ls exit %d; stderr=%s", code, stderr)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("parsing ls JSON: %v", err)
	}
	if len(items) != n {
		t.Errorf("listed %d files, want all %d — pagination must be followed", len(items), n)
	}

	// One page for the root (to find /data) plus one for /data itself. At the
	// old default of 10 per page, /data alone would have taken 9.
	spent := env.srv.ListRequests() - before
	if spent > 2 {
		t.Errorf("listing %d files took %d requests; want 2 (page[size]=100 not applied?)", n, spent)
	}
}

// More files than fit in a single maximum-size page must still be listed in
// full: the next-link walk has to work, not just the big first page.
func TestLs_FolderLargerThanOnePageIsListedInFull(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/many")
	const n = 250 // > 2 maximum pages
	for i := 0; i < n; i++ {
		env.srv.AddFile("abc12", fmt.Sprintf("/many/f%03d.csv", i), []byte("x"))
	}

	stdout, stderr, code := env.run("ls", "abc12:/many", "--output=json")
	if code != 0 {
		t.Fatalf("ls exit %d; stderr=%s", code, stderr)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("parsing ls JSON: %v", err)
	}
	if len(items) != n {
		t.Errorf("listed %d files, want %d — the links.next walk dropped items", len(items), n)
	}
}

// A manifest scan over a large folder must also stay on the cheap path.
func TestStatus_LargeManifestScanIsPageEfficient(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.srv.AddFolder("abc12", "/data")
	const n = 40
	toml := "[project]\nid = \"abc12\"\n"
	for i := 0; i < n; i++ {
		remote := fmt.Sprintf("/data/f%02d.csv", i)
		f := env.srv.AddFile("abc12", remote, []byte("content"))
		env.writeFile(fmt.Sprintf("data/f%02d.csv", i), "content")
		toml += fmt.Sprintf("\n[[files]]\nlocal = \"data/f%02d.csv\"\nremote = %q\nversion = 1\nmd5 = %q\n",
			i, remote, f.VersionMD5(1))
	}
	env.writeFile(".gosf/gosf.toml", toml)

	before := env.srv.ListRequests()
	_, stderr, code := env.run("status")
	if code != 0 {
		t.Fatalf("status exit %d; stderr=%s", code, stderr)
	}
	// The caching lister collapses the shared directory to a single listing;
	// with a 100-item page that is 2 requests total for 40 tracked files.
	if spent := env.srv.ListRequests() - before; spent > 2 {
		t.Errorf("scanning %d files took %d listing requests, want 2", n, spent)
	}
}
