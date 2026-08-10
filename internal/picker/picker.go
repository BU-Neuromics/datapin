package picker

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BU-Neuromics/datapin/internal/gitutil"
	"github.com/BU-Neuromics/datapin/internal/output"
)

// ErrCanceled is returned by Run when the user quits without confirming.
var ErrCanceled = errors.New("selection canceled")

// Run shows the interactive file-tree picker for the given candidates and
// returns the selected file paths. The TUI renders on stderr (stdout is reserved
// for data). Returns ErrCanceled if the user quits with q/esc/ctrl+c.
func Run(cands []gitutil.Candidate) ([]string, error) {
	if len(cands) == 0 {
		return nil, nil
	}
	m := newModel(BuildTree(cands))
	final, err := tea.NewProgram(m, tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		return nil, err
	}
	fm := final.(model)
	if fm.canceled {
		return nil, ErrCanceled
	}
	return fm.root.Selected(), nil
}

type model struct {
	root     *Node
	rows     []*Node
	cursor   int
	offset   int // first visible row (scrolling)
	height   int // rows available for the list
	canceled bool
}

func newModel(root *Node) model {
	return model{root: root, rows: root.Flatten(), height: 20}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Reserve a few lines for the header/footer.
		m.height = msg.Height - 4
		if m.height < 1 {
			m.height = 1
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			return m, tea.Quit // confirm current selection
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case "right", "l":
			if n := m.current(); n != nil && n.IsDir {
				n.Collapsed = false
			}
		case "left", "h":
			if n := m.current(); n != nil && n.IsDir {
				n.Collapsed = true
			}
		case " ":
			if n := m.current(); n != nil {
				n.Toggle()
			}
		case "a":
			m.root.SetAll(true)
		case "n":
			m.root.SetAll(false)
		}
	}

	m.rows = m.root.Flatten()
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.scrollToCursor()
	return m, nil
}

func (m model) current() *Node {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor]
}

func (m *model) scrollToCursor() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

var (
	cursorStyle = lipgloss.NewStyle().Bold(true)
	dirStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	dimStyle    = lipgloss.NewStyle().Faint(true)
)

func (m model) View() string {
	var b strings.Builder
	b.WriteString(cursorStyle.Render("Select files to push to OSF") + "\n")
	b.WriteString(dimStyle.Render("↑/↓ move · ←/→ collapse/expand · space select · a all · n none · enter confirm · q cancel") + "\n\n")

	end := m.offset + m.height
	if end > len(m.rows) {
		end = len(m.rows)
	}
	for i := m.offset; i < end; i++ {
		b.WriteString(m.renderRow(i) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("%d file(s) selected", len(m.root.Selected()))))
	return b.String()
}

func (m model) renderRow(i int) string {
	n := m.rows[i]
	cursor := "  "
	if i == m.cursor {
		cursor = "▶ "
	}
	box := "[ ]"
	switch n.State() {
	case Checked:
		box = "[x]"
	case Partial:
		box = "[~]"
	}
	indent := strings.Repeat("  ", n.Depth)

	label := n.Name
	if n.IsDir {
		arrow := "▸"
		if !n.Collapsed {
			arrow = "▾"
		}
		label = dirStyle.Render(arrow + " " + n.Name + "/")
	} else {
		label += "  " + dimStyle.Render(output.FormatSize(n.Size))
	}

	line := cursor + box + " " + indent + label
	if i == m.cursor {
		return cursorStyle.Render(line)
	}
	return line
}
