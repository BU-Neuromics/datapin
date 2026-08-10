// Package picker provides an interactive file-tree selector for gosf onboard:
// a pure, testable tree model (this file) and a thin bubbletea view over it.
package picker

import (
	"sort"
	"strings"

	"github.com/BU-Neuromics/datapin/internal/gitutil"
)

// CheckState is a node's checkbox state.
type CheckState int

const (
	Unchecked CheckState = iota
	Checked
	Partial // a directory with only some descendants checked
)

// Node is a file or directory in the candidate tree.
type Node struct {
	Name      string  // path segment (base name)
	Path      string  // full slash path from the root ("" for the synthetic root)
	IsDir     bool    //
	Size      int64   // bytes, for files
	Depth     int     // indentation depth; top-level entries are 0 (root is -1)
	Children  []*Node //
	Checked   bool    // files only; directory state is derived via State()
	Collapsed bool    // directories only; dirs start collapsed
}

// BuildTree assembles a directory tree from candidate file paths. The returned
// root is synthetic (Depth -1, not displayed); its children are the top level.
// Directories start collapsed. Children are sorted directories-first, then by name.
func BuildTree(cands []gitutil.Candidate) *Node {
	root := &Node{IsDir: true, Depth: -1}
	dirs := map[string]*Node{"": root}
	for _, c := range cands {
		segs := strings.Split(c.Path, "/")
		cur := root
		curPath := ""
		for i, seg := range segs {
			if curPath == "" {
				curPath = seg
			} else {
				curPath += "/" + seg
			}
			if i == len(segs)-1 {
				cur.Children = append(cur.Children, &Node{
					Name: seg, Path: c.Path, IsDir: false, Size: c.Size, Depth: cur.Depth + 1,
				})
				continue
			}
			child := dirs[curPath]
			if child == nil {
				child = &Node{Name: seg, Path: curPath, IsDir: true, Depth: cur.Depth + 1, Collapsed: true}
				dirs[curPath] = child
				cur.Children = append(cur.Children, child)
			}
			cur = child
		}
	}
	sortTree(root)
	return root
}

func sortTree(n *Node) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir // directories first
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		if c.IsDir {
			sortTree(c)
		}
	}
}

// State returns the checkbox state of n (derived by aggregation for directories).
func (n *Node) State() CheckState {
	if !n.IsDir {
		if n.Checked {
			return Checked
		}
		return Unchecked
	}
	total, checked := 0, 0
	n.eachFile(func(f *Node) {
		total++
		if f.Checked {
			checked++
		}
	})
	switch {
	case total == 0 || checked == 0:
		return Unchecked
	case checked == total:
		return Checked
	default:
		return Partial
	}
}

// Toggle flips a file's checkbox, or for a directory checks all descendant files
// when it is not already fully checked, else unchecks them all.
func (n *Node) Toggle() {
	if !n.IsDir {
		n.Checked = !n.Checked
		return
	}
	target := n.State() != Checked
	n.setAll(target)
}

// SetAll sets the checked state of every descendant file. Used for "all"/"none".
func (n *Node) SetAll(checked bool) { n.setAll(checked) }

func (n *Node) setAll(checked bool) {
	n.eachFile(func(f *Node) { f.Checked = checked })
}

func (n *Node) eachFile(fn func(*Node)) {
	if !n.IsDir {
		fn(n)
		return
	}
	for _, c := range n.Children {
		c.eachFile(fn)
	}
}

// Flatten returns the visible nodes in display order, skipping the children of
// collapsed directories. The synthetic root is not included.
func (n *Node) Flatten() []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(node *Node) {
		for _, c := range node.Children {
			out = append(out, c)
			if c.IsDir && !c.Collapsed {
				walk(c)
			}
		}
	}
	walk(n)
	return out
}

// Selected returns the paths of all checked files, sorted.
func (n *Node) Selected() []string {
	var out []string
	n.eachFile(func(f *Node) {
		if f.Checked {
			out = append(out, f.Path)
		}
	})
	sort.Strings(out)
	return out
}
