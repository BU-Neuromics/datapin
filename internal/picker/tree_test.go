package picker

import (
	"reflect"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/gitutil"
)

func sampleTree() *Node {
	return BuildTree([]gitutil.Candidate{
		{Path: "data/raw/a.csv", Size: 10},
		{Path: "data/raw/b.csv", Size: 20},
		{Path: "data/notes.txt", Size: 5},
		{Path: "top.txt", Size: 1},
	})
}

// find returns the first node with the given path.
func find(root *Node, path string) *Node {
	var out *Node
	var walk func(*Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			if c.Path == path {
				out = c
			}
			walk(c)
		}
	}
	walk(root)
	return out
}

func flatPaths(root *Node) []string {
	var p []string
	for _, n := range root.Flatten() {
		p = append(p, n.Path)
	}
	return p
}

func TestBuildTree_StructureAndOrder(t *testing.T) {
	root := sampleTree()
	// Top level: dir "data" before file "top.txt".
	if len(root.Children) != 2 || root.Children[0].Path != "data" || root.Children[1].Path != "top.txt" {
		t.Fatalf("unexpected top level: %+v", root.Children)
	}
	if root.Children[0].Depth != 0 || find(root, "data/raw/a.csv").Depth != 2 {
		t.Errorf("unexpected depths")
	}
}

func TestFlatten_RespectsCollapse(t *testing.T) {
	root := sampleTree()
	// Dirs collapsed by default → only top-level entries visible.
	if got := flatPaths(root); !reflect.DeepEqual(got, []string{"data", "top.txt"}) {
		t.Fatalf("collapsed flatten = %v", got)
	}
	// Expand data → its children appear; data/raw still collapsed.
	find(root, "data").Collapsed = false
	if got := flatPaths(root); !reflect.DeepEqual(got, []string{"data", "data/raw", "data/notes.txt", "top.txt"}) {
		t.Fatalf("expanded flatten = %v", got)
	}
	find(root, "data/raw").Collapsed = false
	if got := flatPaths(root); !reflect.DeepEqual(got,
		[]string{"data", "data/raw", "data/raw/a.csv", "data/raw/b.csv", "data/notes.txt", "top.txt"}) {
		t.Fatalf("fully expanded flatten = %v", got)
	}
}

func TestToggle_FileAndSelected(t *testing.T) {
	root := sampleTree()
	find(root, "top.txt").Toggle()
	if got := root.Selected(); !reflect.DeepEqual(got, []string{"top.txt"}) {
		t.Errorf("selected = %v, want [top.txt]", got)
	}
}

func TestToggle_DirCascadesAndPartial(t *testing.T) {
	root := sampleTree()
	data := find(root, "data")

	// Toggling a dir checks all descendant files.
	data.Toggle()
	if data.State() != Checked {
		t.Errorf("data state = %v, want Checked", data.State())
	}
	want := []string{"data/notes.txt", "data/raw/a.csv", "data/raw/b.csv"}
	if got := root.Selected(); !reflect.DeepEqual(got, want) {
		t.Errorf("selected = %v, want %v", got, want)
	}

	// Unchecking one file makes the dir Partial.
	find(root, "data/notes.txt").Toggle()
	if data.State() != Partial {
		t.Errorf("data state = %v, want Partial", data.State())
	}
	if find(root, "data/raw").State() != Checked {
		t.Errorf("data/raw should still be fully Checked")
	}

	// Toggling a partial dir checks everything again.
	data.Toggle()
	if data.State() != Checked {
		t.Errorf("re-toggled data state = %v, want Checked", data.State())
	}

	// Toggling a fully-checked dir unchecks all.
	data.Toggle()
	if data.State() != Unchecked || len(root.Selected()) != 0 {
		t.Errorf("expected all unchecked, state=%v selected=%v", data.State(), root.Selected())
	}
}

func TestSetAll(t *testing.T) {
	root := sampleTree()
	root.SetAll(true)
	if len(root.Selected()) != 4 {
		t.Errorf("SetAll(true) selected %d, want 4", len(root.Selected()))
	}
	root.SetAll(false)
	if len(root.Selected()) != 0 {
		t.Errorf("SetAll(false) selected %d, want 0", len(root.Selected()))
	}
}
