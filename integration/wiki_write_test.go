//go:build integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWikiPush_CreateAndCanonicalRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Wiki Project")
	// Push content with CRLF and a trailing newline. OSF (and the fake) store the
	// canonical form; datapin compares canonically, so this is what round-trips.
	e.writeFile("Protocol.md", "# Protocol\r\nstep one\n\nno trailing newline\n")

	stdout, stderr, code := e.run("wiki", "push", "Protocol.md", "abc12", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var r struct {
		Project string `json:"project"`
		Page    string `json:"page"`
		Action  string `json:"action"`
		Version int    `json:"version"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if r.Action != "create" || r.Page != "Protocol" || r.Version != 1 {
		t.Errorf("result = %+v", r)
	}

	// Stored content is the canonical form (CRLF→LF, surrounding whitespace trimmed).
	p := e.srv.GetWiki("abc12", "Protocol")
	if p == nil {
		t.Fatal("page not created on server")
	}
	want := "# Protocol\nstep one\n\nno trailing newline"
	if string(p.LatestContent()) != want {
		t.Errorf("stored content = %q, want canonical %q", p.LatestContent(), want)
	}

	// Re-pushing the same file is idempotent despite the CRLF/trailing-newline
	// difference from what OSF stored — the canonical forms match.
	stdout, _, code = e.run("wiki", "push", "Protocol.md", "abc12", "--output=json")
	if code != 0 {
		t.Fatalf("second push exit %d", code)
	}
	json.Unmarshal([]byte(stdout), &r)
	if r.Action != "skip" {
		t.Errorf("idempotent re-push action = %q, want skip", r.Action)
	}
}

func TestWikiPush_UpdateAndIdempotentSkip(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Wiki Project")
	e.srv.AddWiki("abc12", "home", []byte("old\n"))
	e.writeFile("home.md", "new\n")

	// Different content → new version.
	stdout, stderr, code := e.run("wiki", "push", "home.md", "abc12:home", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var r struct {
		Action  string `json:"action"`
		Version int    `json:"version"`
	}
	json.Unmarshal([]byte(stdout), &r)
	if r.Action != "update" || r.Version != 2 {
		t.Errorf("result = %+v", r)
	}
	if got := string(e.srv.GetWiki("abc12", "home").LatestContent()); got != "new" {
		t.Errorf("stored = %q", got)
	}

	// Identical content → skip, no new version minted.
	stdout, _, code = e.run("wiki", "push", "home.md", "abc12:home", "--output=json")
	if code != 0 {
		t.Fatalf("second push exit %d", code)
	}
	json.Unmarshal([]byte(stdout), &r)
	if r.Action != "skip" {
		t.Errorf("second push action = %q, want skip", r.Action)
	}
	if v := e.srv.GetWiki("abc12", "home").LatestVersion(); v != 2 {
		t.Errorf("version after idempotent push = %d, want 2 (no redundant version)", v)
	}
}

func TestWikiPush_DryRun(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Wiki Project")
	e.writeFile("notes.md", "body\n")

	stdout, _, code := e.run("wiki", "push", "notes.md", "abc12", "--dry-run", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var r struct {
		Action string `json:"action"`
		DryRun bool   `json:"dry_run"`
	}
	json.Unmarshal([]byte(stdout), &r)
	if !r.DryRun || r.Action != "create" {
		t.Errorf("result = %+v", r)
	}
	if e.srv.GetWiki("abc12", "notes") != nil {
		t.Error("dry-run must not create the page")
	}
}

func TestWikiPush_RequiresToken(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Wiki Project")
	e.writeFile("notes.md", "body\n")

	_, stderr, code := e.runClean("", "wiki", "push", "notes.md", "abc12")
	if code == 0 || !strings.Contains(stderr, "auth") {
		t.Errorf("expected auth requirement, code=%d stderr=%s", code, stderr)
	}
}

func TestWikiRm(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Wiki Project")
	e.srv.AddWiki("abc12", "home", []byte("h"))
	e.srv.AddWiki("abc12", "scratch", []byte("s"))

	// JSON without --yes refuses.
	_, stderr, code := e.run("wiki", "rm", "abc12:scratch", "--output=json")
	if code == 0 || !strings.Contains(stderr, "--yes") {
		t.Errorf("expected --yes refusal, code=%d stderr=%s", code, stderr)
	}

	// Dry run deletes nothing.
	stdout, _, code := e.run("wiki", "rm", "abc12:scratch", "--dry-run", "--output=json")
	if code != 0 {
		t.Fatalf("dry-run exit %d", code)
	}
	if e.srv.GetWiki("abc12", "scratch") == nil {
		t.Fatal("dry-run must not delete")
	}

	// Real deletion with --yes.
	stdout, stderr, code = e.run("wiki", "rm", "abc12:scratch", "--yes", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var r struct {
		Node   string `json:"node"`
		Page   string `json:"page"`
		DryRun bool   `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if r.Node != "abc12" || r.Page != "scratch" || r.DryRun {
		t.Errorf("result = %+v", r)
	}
	if e.srv.GetWiki("abc12", "scratch") != nil {
		t.Error("page still exists after rm")
	}
	if len(e.srv.WikiDeletes()) != 1 {
		t.Errorf("deletes = %v", e.srv.WikiDeletes())
	}
}

func TestWikiRm_HomeRefusedClientSide(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Wiki Project")
	e.srv.AddWiki("abc12", "home", []byte("h"))

	_, stderr, code := e.run("wiki", "rm", "abc12:home", "--yes")
	if code == 0 {
		t.Fatal("expected refusal")
	}
	if !strings.Contains(stderr, "home") || !strings.Contains(stderr, "cannot be deleted") {
		t.Errorf("stderr = %s", stderr)
	}
	if len(e.srv.WikiDeletes()) != 0 {
		t.Error("no DELETE should reach the server")
	}
}

func TestWikiMv(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Wiki Project")
	e.srv.AddWiki("abc12", "draft", []byte("d"))

	stdout, stderr, code := e.run("wiki", "mv", "abc12:draft", "final", "--output=json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var r struct {
		Node   string `json:"node"`
		From   string `json:"from"`
		To     string `json:"to"`
		DryRun bool   `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if r.From != "draft" || r.To != "final" {
		t.Errorf("result = %+v", r)
	}
	if e.srv.GetWiki("abc12", "final") == nil || e.srv.GetWiki("abc12", "draft") != nil {
		t.Error("rename not applied on server")
	}
}

func TestWikiMv_Guards(t *testing.T) {
	e := newTestEnv(t)
	e.srv.AddProject("abc12", "Wiki Project")
	e.srv.AddWiki("abc12", "home", []byte("h"))
	e.srv.AddWiki("abc12", "a", []byte("a"))
	e.srv.AddWiki("abc12", "b", []byte("b"))

	// Renaming home is refused client-side.
	_, stderr, code := e.run("wiki", "mv", "abc12:home", "start")
	if code == 0 || !strings.Contains(stderr, "cannot be renamed") {
		t.Errorf("expected home rename refusal, code=%d stderr=%s", code, stderr)
	}

	// Renaming onto an existing name surfaces the server conflict.
	_, stderr, code = e.run("wiki", "mv", "abc12:a", "b")
	if code == 0 || !strings.Contains(stderr, "already exists") {
		t.Errorf("expected conflict, code=%d stderr=%s", code, stderr)
	}

	// Invalid new name (slash) is refused client-side.
	_, stderr, code = e.run("wiki", "mv", "abc12:a", "x/y")
	if code == 0 || !strings.Contains(stderr, "forward slashes") {
		t.Errorf("expected name validation, code=%d stderr=%s", code, stderr)
	}

	// Dry run applies nothing.
	_, _, code = e.run("wiki", "mv", "abc12:a", "c", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run exit %d", code)
	}
	if e.srv.GetWiki("abc12", "a") == nil {
		t.Error("dry-run must not rename")
	}
}
