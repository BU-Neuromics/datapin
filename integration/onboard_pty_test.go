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

// TestOnboardPublish_PTY_EndToEnd drives the rewritten onboard command over a
// pseudo-terminal, end to end through the publish flow: pick "another
// InvenioRDM instance" and point it at fakeinvenio → name it, store a token →
// select all files in the tree picker → slug, title, creators, explicit
// license choice, contact e-mail → assert the manifest, config, and token
// file. Skips where a PTY can't be allocated so it never blocks CI.
func TestOnboardPublish_PTY_EndToEnd(t *testing.T) {
	e := newInvenioEnv(t)
	writeFileUnder(t, e.dir, "data.csv", "col\n1\n")
	writeFileUnder(t, e.dir, "notes/todo.txt", "x")

	cmd := exec.Command(binaryPath, "onboard", "--no-keychain")
	cmd.Dir = e.dir
	cmd.Env = append(hermeticEnv(),
		"HOME="+e.dir,
		"XDG_CONFIG_HOME="+filepath.Join(e.dir, ".config"),
		"TERM=xterm",
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("cannot allocate a PTY: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})

	term := newTermReader(ptmx)

	// Remote menu, sandbox rehearsal first → pick "another InvenioRDM
	// instance" and point it at the fake.
	term.expect(t, "Where will you publish?", 5*time.Second)
	term.expect(t, "Zenodo Sandbox", time.Second)
	_, _ = ptmx.WriteString("3\n")
	term.expect(t, "Repository URL", 5*time.Second)
	_, _ = ptmx.WriteString(e.srv.URL() + "\n")
	term.expect(t, "Name for this remote", 5*time.Second)
	_, _ = ptmx.WriteString("sandbox\n")
	term.expect(t, "API token", 5*time.Second)
	_, _ = ptmx.WriteString(invenioTestToken + "\n")
	term.expect(t, "added remote", 5*time.Second)

	// Tree picker → select all, confirm.
	term.expect(t, "Select the files this dataset will contain", 5*time.Second)
	_, _ = ptmx.WriteString("a")
	_, _ = ptmx.WriteString("\r")

	// Slug + the DataCite floor.
	term.expect(t, "Dataset slug", 5*time.Second)
	_, _ = ptmx.WriteString("rnaseq\n")
	term.expect(t, "Title", 5*time.Second)
	_, _ = ptmx.WriteString("Test Dataset\n")
	term.expect(t, "Description", 5*time.Second)
	_, _ = ptmx.WriteString("\n")
	term.expect(t, "Creator name", 5*time.Second)
	_, _ = ptmx.WriteString("Doe, Jane\n")
	term.expect(t, "ORCID", 5*time.Second)
	_, _ = ptmx.WriteString("0000-0002-1825-0097\n")
	term.expect(t, "Affiliation", 5*time.Second)
	_, _ = ptmx.WriteString("\n")
	term.expect(t, "Add another creator?", 5*time.Second)
	_, _ = ptmx.WriteString("\n")

	// License is an explicit choice — CC0-1.0 suggested, never prefilled.
	term.expect(t, "CC0-1.0", 5*time.Second)
	term.expect(t, "License [1-4]", 5*time.Second)
	_, _ = ptmx.WriteString("1\n")
	term.expect(t, "Contact e-mail", 5*time.Second)
	_, _ = ptmx.WriteString("\n")

	// The workspace track is offered as an opt-in second track — take it,
	// pointed at a local directory.
	term.expect(t, "Add a workspace remote now?", 5*time.Second)
	_, _ = ptmx.WriteString("y\n")
	term.expect(t, "Workspace kind [1-3]", 5*time.Second)
	_, _ = ptmx.WriteString("1\n")
	term.expect(t, "Workspace URL", 5*time.Second)
	_, _ = ptmx.WriteString(filepath.Join(e.dir, "ws") + "\n")
	term.expect(t, "Name for this remote", 5*time.Second)
	_, _ = ptmx.WriteString("\n") // accept the default name
	term.expect(t, "added workspace remote", 5*time.Second)

	// Summary points at check + publish.
	term.expect(t, "Next steps", 5*time.Second)
	term.expect(t, "datapin publish rnaseq", time.Second)

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("onboard did not exit")
	}

	// The manifest gained the dataset with its metadata and files.
	toml := readFileUnder(t, e.dir, ".datapin/datapin.toml")
	for _, want := range []string{
		`default_archive = 'sandbox'`,
		`default_workspace = 'workspace'`,
		`slug = 'rnaseq'`,
		`title = 'Test Dataset'`,
		`license = 'CC0-1.0'`,
		`name = 'Doe, Jane'`,
		`orcid = '0000-0002-1825-0097'`,
		"data.csv",
		"notes/todo.txt",
	} {
		if !strings.Contains(toml, want) {
			t.Errorf("manifest missing %q:\n%s", want, toml)
		}
	}

	// The remote landed in config.toml and its token in the file store.
	cfg := readFileUnder(t, e.dir, ".config/datapin/config.toml")
	if !strings.Contains(cfg, "sandbox") || !strings.Contains(cfg, e.srv.URL()) {
		t.Errorf("config.toml missing the sandbox remote:\n%s", cfg)
	}
	tok := readFileUnder(t, e.dir, ".config/datapin/tokens/sandbox")
	if strings.TrimSpace(tok) != invenioTestToken {
		t.Errorf("token file = %q, want %q", tok, invenioTestToken)
	}
}

// TestOnboardOSF_PTY_EndToEnd drives the legacy OSF flow (behind --osf, with a
// deprecation notice) over a pseudo-terminal: authenticate (skipped via
// OSF_TOKEN) → type a project GUID → select all files in the tree picker →
// accept the default remote base → assert the manifest. Skips where a PTY
// can't be allocated so it never blocks CI.
func TestOnboardOSF_PTY_EndToEnd(t *testing.T) {
	env := newTestEnv(t)
	env.srv.AddProject("abc12", "Test Project")
	env.writeFile("data.csv", "col\n1\n")
	env.writeFile("notes/todo.txt", "x")

	cmd := exec.Command(binaryPath, "onboard", "--osf")
	cmd.Dir = env.dir
	cmd.Env = append(hermeticEnv(),
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

	// The escape hatch announces its deprecation.
	term.expect(t, "deprecated", 5*time.Second)

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

// writeFileUnder/readFileUnder mirror testEnv.writeFile/readFile for envs
// that don't carry them (invenioEnv).
func writeFileUnder(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFileUnder(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}
