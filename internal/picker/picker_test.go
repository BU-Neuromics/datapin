package picker

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BU-Neuromics/datapin/internal/gitutil"
)

func send(m model, msg tea.KeyMsg) model {
	nm, _ := m.Update(msg)
	return nm.(model)
}

func keyType(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }
func keyRune(r rune) tea.KeyMsg        { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func pickerModel() model {
	return newModel(BuildTree([]gitutil.Candidate{
		{Path: "data/raw/a.csv", Size: 10},
		{Path: "data/notes.txt", Size: 5},
		{Path: "top.txt", Size: 1},
	}))
}

func TestPicker_ExpandNavigateSelect(t *testing.T) {
	m := pickerModel()
	// Collapsed: only top-level rows.
	if len(m.rows) != 2 {
		t.Fatalf("initial rows = %d, want 2 (data, top.txt)", len(m.rows))
	}

	// Expand "data" (cursor 0) → data, data/raw, data/notes.txt, top.txt = 4.
	m = send(m, keyType(tea.KeyRight))
	if len(m.rows) != 4 {
		t.Fatalf("after expanding data, rows = %d, want 4", len(m.rows))
	}

	// Move to data/raw and expand it.
	m = send(m, keyType(tea.KeyDown)) // cursor 1 = data/raw
	m = send(m, keyType(tea.KeyRight))
	if len(m.rows) != 5 {
		t.Fatalf("after expanding data/raw, rows = %d, want 5", len(m.rows))
	}

	// Space on data/raw checks its one descendant file.
	m = send(m, keyType(tea.KeySpace))
	if got := m.root.Selected(); len(got) != 1 || got[0] != "data/raw/a.csv" {
		t.Errorf("selected = %v, want [data/raw/a.csv]", got)
	}

	// Collapse data/raw again.
	m = send(m, keyType(tea.KeyLeft))
	if len(m.rows) != 4 {
		t.Errorf("after collapsing data/raw, rows = %d, want 4", len(m.rows))
	}
}

func TestPicker_AllNone(t *testing.T) {
	m := pickerModel()
	m = send(m, keyRune('a'))
	if len(m.root.Selected()) != 3 {
		t.Errorf("'a' selected %d, want 3", len(m.root.Selected()))
	}
	m = send(m, keyRune('n'))
	if len(m.root.Selected()) != 0 {
		t.Errorf("'n' selected %d, want 0", len(m.root.Selected()))
	}
}

func TestPicker_ConfirmVsCancel(t *testing.T) {
	if send(pickerModel(), keyType(tea.KeyEnter)).canceled {
		t.Error("enter should confirm, not cancel")
	}
	if !send(pickerModel(), keyType(tea.KeyEsc)).canceled {
		t.Error("esc should cancel")
	}
}
