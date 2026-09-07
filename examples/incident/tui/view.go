package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	sevStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	dimStyle     = lipgloss.NewStyle().Faint(true)
	selStyle     = lipgloss.NewStyle().Reverse(true)
	cursorStyle  = lipgloss.NewStyle().Reverse(true)
	focusBorder  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("6"))
	blurBorder   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8"))
	statusStyle  = lipgloss.NewStyle().Faint(true)
	presentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
)

func (m *Model) View() string {
	if m.width == 0 {
		return "connecting..."
	}
	leftWidth := min(46, m.width/2)
	rightWidth := m.width - leftWidth - 6 // borders and padding
	bodyHeight := max(6, m.height-4)

	left := m.viewIncident(leftWidth, bodyHeight)
	right := m.viewNotes(rightWidth, bodyHeight)

	leftBox, rightBox := blurBorder, blurBorder
	if m.focus == paneTasks {
		leftBox = focusBorder
	} else {
		rightBox = focusBorder
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		leftBox.Width(leftWidth).Height(bodyHeight).Render(left),
		rightBox.Width(rightWidth).Height(bodyHeight).Render(right),
	)

	return lipgloss.JoinVertical(lipgloss.Left, body, m.viewPresence(), m.viewStatus())
}

func (m *Model) viewIncident(width, height int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", sevStyle.Render(m.inc.Severity.String()), titleStyle.Render(m.inc.Title))
	fmt.Fprintf(&b, "%s · %s", m.inc.ID, m.inc.Status)
	if m.inc.Commander != "" {
		fmt.Fprintf(&b, " · IC: %s", m.inc.Commander)
	}
	b.WriteString("\n")
	if len(m.inc.Services) > 0 {
		fmt.Fprintf(&b, "%s\n", dimStyle.Render("services: "+strings.Join(m.inc.Services, ", ")))
	}
	b.WriteString("\n")

	if len(m.inc.Tasks) == 0 {
		b.WriteString(dimStyle.Render("no tasks") + "\n")
	}
	for i, t := range m.inc.Tasks {
		mark := "[ ]"
		if t.Done {
			mark = "[x]"
		}
		line := fmt.Sprintf("%s %s: %s", mark, t.ID, t.Text)
		if t.Owner != "" {
			line += " @" + t.Owner
		}
		if len(line) > width-2 {
			line = line[:width-3] + "…"
		}
		if i == m.sel && m.focus == paneTasks {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n" + dimStyle.Render("c claim · d done · 1-4 escalate\nM/R/C status · U undo · q quit"))
	return b.String()
}

func (m *Model) viewNotes(width, height int) string {
	text := m.notes.Text()
	runes := []rune(text)
	cursor := min(m.cursor, len(runes))

	// Render with the cursor as an inverted cell.
	var b strings.Builder
	b.WriteString(dimStyle.Render("shared notes — everyone's keystrokes merge") + "\n\n")
	for i, r := range runes {
		if i == cursor && m.focus == paneNotes {
			if r == '\n' {
				b.WriteString(cursorStyle.Render(" ") + "\n")
				continue
			}
			b.WriteString(cursorStyle.Render(string(r)))
			continue
		}
		b.WriteRune(r)
	}
	if cursor == len(runes) && m.focus == paneNotes {
		b.WriteString(cursorStyle.Render(" "))
	}
	return lipgloss.NewStyle().Width(width).MaxHeight(height).Render(b.String())
}

// viewPresence draws who is here, from the awareness view: names appear when
// peers announce and fade when their heartbeats stop.
func (m *Model) viewPresence() string {
	states := m.notes.Awareness().States()
	names := make([]string, 0, len(states))
	for node, p := range states {
		label := p.Name
		if label == "" {
			label = node
		}
		names = append(names, fmt.Sprintf("● %s@%d", label, p.Pos))
	}
	sort.Strings(names)
	if len(names) == 0 {
		return dimStyle.Render(" nobody else here")
	}
	return presentStyle.Render(" " + strings.Join(names, "  "))
}

func (m *Model) viewStatus() string {
	return statusStyle.Render(" " + m.status + " · tab to switch panes")
}
