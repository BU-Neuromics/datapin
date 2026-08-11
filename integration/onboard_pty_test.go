//go:build integration

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

var ptyANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// termReader drains a PTY in the background so expect() can poll accumulated,
// ANSI-stripped output without blocking on reads. It also answers the terminal
// queries a TUI issues (background color, cursor position, device attributes) —
// a bare PTY isn't a terminal emulator, so without these lipgloss/bubbletea
// would block waiting for responses.
type termReader struct {
	mu  sync.Mutex
	buf []byte
}

func newTermReader(pt *os.File) *termReader {
	tr := &termReader{}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := pt.Read(b)
			if n > 0 {
				chunk := b[:n]
				tr.mu.Lock()
				tr.buf = append(tr.buf, chunk...)
				tr.mu.Unlock()
				answerQueries(pt, chunk)
			}
			if err != nil {
				return
			}
		}
	}()
	return tr
}

// answerQueries replies to the terminal queries a real emulator would answer.
func answerQueries(pt *os.File, chunk []byte) {
	if bytes.Contains(chunk, []byte("\x1b]11;?")) { // OSC 11: background color
		_, _ = pt.Write([]byte("\x1b]11;rgb:0000/0000/0000\x07"))
	}
	if bytes.Contains(chunk, []byte("\x1b[6n")) { // CPR: cursor position
		_, _ = pt.Write([]byte("\x1b[1;1R"))
	}
	if bytes.Contains(chunk, []byte("\x1b[c")) { // DA1: device attributes
		_, _ = pt.Write([]byte("\x1b[?1;2c"))
	}
}

func (tr *termReader) text() string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return ptyANSI.ReplaceAllString(string(tr.buf), "")
}

func (tr *termReader) expect(t *testing.T, sub string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(tr.text(), sub) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q; transcript so far:\n%s", sub, tr.text())
}

// TestOnboard_PTY_EndToEnd drives the real onboard command over a pseudo-terminal:
// authenticate (skipped via OSF_TOKEN) → type a project GUID → select all files
// in the tree picker → accept the default remote base → assert the manifest.
// Skips where a PTY can't be allocated so it never blocks CI.
func TestOnboard_PTY_EndToEnd(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile("data.csv", "col\n1\n")
	env.writeFile("notes/todo.txt", "x")

	cmd := exec.Command(binaryPath, "onboard")
	cmd.Dir = env.dir
	cmd.Env = append(os.Environ(),
		"DATAPIN_API_BASE="+env.srv.URL()+"/v2",
		"DATAPIN_FILES_BASE="+env.srv.URL(),
		"OSF_TOKEN=test-token", // skips the auth prompt
		"HOME="+env.dir,
		"XDG_CONFIG_HOME="+filepath.Join(env.dir, ".config"),
		"TERM=xterm",
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("cannot allocate a PTY: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 100})

	term := newTermReader(ptmx)

	// Project prompt → type the GUID.
	term.expect(t, "project GUID", 5*time.Second)
	_, _ = ptmx.WriteString("abc12\n")

	// Tree picker rendered → select all, then confirm.
	term.expect(t, "Select files to push", 5*time.Second)
	_, _ = ptmx.WriteString("a") // select all
	_, _ = ptmx.WriteString("\r")

	// Remote-base prompt → accept default "/".
	term.expect(t, "Remote base path", 5*time.Second)
	_, _ = ptmx.WriteString("\n")

	// Summary confirms completion.
	term.expect(t, "Next steps", 5*time.Second)

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("onboard did not exit")
	}

	// The manifest gained push entries for both files.
	toml := env.readFile(".datapin/datapin.toml")
	for _, want := range []string{"data.csv", "notes/todo.txt"} {
		if !strings.Contains(toml, want) {
			t.Errorf("manifest missing %q:\n%s", want, toml)
		}
	}
	if strings.Contains(toml, "direction") {
		t.Errorf("direction is no longer written to the manifest:\n%s", toml)
	}
}
