//go:build live

// Real-protocol tier: the same journal semantics the in-process-server
// unit tests pin down, run against a real OpenSSH sshd (the atmoz/sftp
// container in CI — the workspace-protocols job in ci.yml). The pipe-based
// unit tests never touch ssh at all, so this is the only tier that
// exercises Dial's auth ladder and known_hosts verification end to end.
// Skips unless DATAPIN_TEST_SFTP_URL is set.
//
// Run locally:
//
//	docker run -d -p 2222:22 atmoz/sftp demo:demopass:::upload
//	DATAPIN_TEST_SFTP_URL=sftp://demo@localhost:2222/upload \
//	  DATAPIN_TEST_SFTP_PASSWORD=demopass \
//	  go test -tags live -count=1 -v ./internal/workspace/sftpws/
package sftpws_test

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BU-Neuromics/datapin/internal/workspace"
	"github.com/BU-Neuromics/datapin/internal/workspace/sftpws"
)

func TestLive_JournalOverRealSSH(t *testing.T) {
	rawURL := os.Getenv("DATAPIN_TEST_SFTP_URL")
	if rawURL == "" {
		t.Skip("DATAPIN_TEST_SFTP_URL unset — skipping real-SSH tier")
	}
	password := os.Getenv("DATAPIN_TEST_SFTP_PASSWORD")
	if password == "" {
		t.Fatal("DATAPIN_TEST_SFTP_PASSWORD must be set alongside the URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing %s: %v", rawURL, err)
	}
	port := u.Port()
	if port == "" {
		port = "22"
	}

	// Dial verifies host keys against $HOME/.ssh/known_hosts — never a
	// silent accept — so build a scratch HOME holding the server's real
	// key, exactly what ssh-keyscan hands a first-time user.
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	keys, err := exec.Command("ssh-keyscan", "-p", port, u.Hostname()).Output()
	if err != nil || len(keys) == 0 {
		t.Fatalf("ssh-keyscan -p %s %s: %v", port, u.Hostname(), err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), keys, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "") // force the password rung of the auth ladder
	t.Setenv("DATAPIN_SFTP_PASSWORD", password)

	// A unique base path per run keeps reruns independent.
	u.Path = fmt.Sprintf("%s/datapin-ci-%d-%d", strings.TrimSuffix(u.Path, "/"), time.Now().UnixNano(), os.Getpid())
	store, err := sftpws.Dial(u.String())
	if err != nil {
		t.Fatalf("Dial(%s): %v", u.String(), err)
	}
	t.Cleanup(func() { _ = store.Close() })

	journalRoundTrip(t, workspace.New(store))
}

// journalRoundTrip asserts the D4 invariant — every version datapin wrote
// is revertible — over a real remote: push twice, read back the first
// version, revert to it, and see it as current.
func journalRoundTrip(t *testing.T, ws *workspace.Workspace) {
	t.Helper()
	ctx := t.Context()

	push := func(content []byte) {
		t.Helper()
		if _, err := ws.Push(ctx, "results/data.csv", strings.NewReader(string(content)), int64(len(content)), md5hex(content), "live-ci"); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	push([]byte("v1"))
	push([]byte("v2 changed"))

	events, err := ws.Versions(ctx, "results/data.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || !events[0].Recoverable || !events[1].Recoverable {
		t.Fatalf("events = %+v", events)
	}

	var buf strings.Builder
	if err := ws.GetVersion(ctx, "results/data.csv", 1, &buf); err != nil {
		t.Fatalf("GetVersion(1): %v", err)
	}
	if buf.String() != "v1" {
		t.Errorf("v1 bytes = %q", buf.String())
	}

	if _, err := ws.Revert(ctx, "results/data.csv", 1, "live-ci", "rollback"); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	buf.Reset()
	if err := ws.Get(ctx, "results/data.csv", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "v1" {
		t.Errorf("after revert current = %q", buf.String())
	}
}
