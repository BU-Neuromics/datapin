package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(cs []Candidate) map[string]bool {
	m := map[string]bool{}
	for _, c := range cs {
		m[c.Path] = true
	}
	return m
}

func TestCandidates_GitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@e.st"}, {"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}

	write(t, root, "tracked.go", "package main")
	write(t, root, ".gitignore", "ignored/\n*.tmp\n")
	write(t, root, "ignored/data.csv", "x")         // git-ignored
	write(t, root, "scratch.tmp", "x")              // git-ignored
	write(t, root, "untracked.csv", "x")            // untracked, not ignored
	write(t, root, ".datapin/datapin.toml", "junk") // must be excluded
	// stage + commit the tracked file so it's "tracked", not "untracked".
	for _, args := range [][]string{{"add", "tracked.go", ".gitignore"}, {"commit", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}

	cs, err := Candidates(root)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	got := paths(cs)
	for _, want := range []string{"ignored/data.csv", "scratch.tmp", "untracked.csv"} {
		if !got[want] {
			t.Errorf("expected candidate %q, got %v", want, cs)
		}
	}
	for _, notWant := range []string{"tracked.go", ".gitignore", ".datapin/datapin.toml"} {
		if got[notWant] {
			t.Errorf("did not expect candidate %q (tracked or excluded)", notWant)
		}
	}
}

func TestCandidates_NonGitDir_FallsBackToAll(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.csv", "aa")
	write(t, root, "sub/b.txt", "bbb")
	write(t, root, ".datapin/datapin.toml", "junk")

	cs, err := Candidates(root)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	got := paths(cs)
	if !got["a.csv"] || !got["sub/b.txt"] {
		t.Errorf("expected all files outside a git repo, got %v", cs)
	}
	if got[".datapin/datapin.toml"] {
		t.Errorf(".datapin must be excluded, got %v", cs)
	}
	// sizes are populated
	for _, c := range cs {
		if c.Path == "sub/b.txt" && c.Size != 3 {
			t.Errorf("size of sub/b.txt = %d, want 3", c.Size)
		}
	}
}
