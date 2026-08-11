// Package gitutil discovers local files that are candidates for pushing to OSF:
// files git does not track (ignored + untracked). Outside a git repository it
// falls back to every file under the root. It shells out to the `git` binary.
package gitutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Candidate is a local file offered for tracking, with a repo-relative,
// slash-separated path and its size in bytes.
type Candidate struct {
	Path string
	Size int64
}

// excludedTop are top-level directories never offered as candidates.
var excludedTop = map[string]bool{".git": true, ".datapin": true}

// Candidates returns the files under root that git does not track (ignored +
// untracked). If root is not a git work tree (or git is unavailable), it returns
// every regular file under root instead. `.git` and `.datapin` are always excluded.
// Results are sorted by path.
func Candidates(root string) ([]Candidate, error) {
	rels, err := gitUntracked(root)
	if err != nil {
		rels, err = walkAll(root)
		if err != nil {
			return nil, err
		}
	}

	var out []Candidate
	seen := map[string]bool{}
	for _, rel := range rels {
		rel = filepath.ToSlash(rel)
		if rel == "" || seen[rel] || excluded(rel) {
			continue
		}
		seen[rel] = true
		var size int64
		if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); statErr == nil {
			if info.IsDir() {
				continue
			}
			size = info.Size()
		}
		out = append(out, Candidate{Path: rel, Size: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// excluded reports whether a repo-relative path is under an excluded top dir.
func excluded(rel string) bool {
	top := rel
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		top = rel[:i]
	}
	return excludedTop[top]
}

// gitUntracked returns files git does not track (ignored + untracked), or an
// error if root is not a git work tree or git is unavailable.
func gitUntracked(root string) ([]string, error) {
	out, err := runGit(root, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		return nil, errNotWorkTree
	}
	untracked, err := runGit(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	ignored, err := runGit(root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	return append(splitNUL(untracked), splitNUL(ignored)...), nil
}

var errNotWorkTree = errNotGit("not a git work tree")

type errNotGit string

func (e errNotGit) Error() string { return string(e) }

func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func splitNUL(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// walkAll returns every regular file under root (repo-relative, slash paths),
// skipping the excluded top-level directories.
func walkAll(root string) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if excludedTop[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rels = append(rels, rel)
		return nil
	})
	return rels, err
}
