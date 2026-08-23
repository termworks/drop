package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"

	"github.com/bresilla/drop/src/pkg/convo"
)

func (m Model) View() string {
	if m.width == 0 {
		return dimStyle.Render("  starting…")
	}

	body := m.list.View()
	if m.at == levelOpen {
		body = m.openView()
	}

	return m.header() + "\n" + body + "\n" + m.footer()
}

// header is where you are and which device you are.
func (m Model) header() string {
	left := brandStyle.Render("▍") + " " + m.where()

	right := faintStyle.Render(m.me.Name)
	if m.live {
		right = goodStyle.Render("● live") + faintStyle.Render("   "+m.me.Name)
	}
	if m.loading {
		right = dimStyle.Render("asking…") + faintStyle.Render("   "+m.me.Name)
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return " " + left
	}
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

// where is the trail of what you entered to get here.
func (m Model) where() string {
	parts := []string{"drop"}

	if m.at >= levelPaths {
		if with, ok := m.peer(); ok {
			parts = append(parts, with.Name)
		}
	}
	if m.at == levelOpen {
		if at, ok := m.path(); ok {
			parts = append(parts, at.Path)
		}
	}
	return crumb(parts...)
}

// openView is whatever is at the path that was entered.
func (m Model) openView() string {
	if m.trouble != "" {
		return "\n " + badStyle.Render("✗ ") + m.trouble
	}

	at, ok := m.path()
	if !ok {
		return ""
	}

	switch kindOf(at) {
	case "chat":
		return m.chatView()

	case "tty", "stream":
		if m.screen == nil {
			return "\n " + faintStyle.Render("not watching.")
		}
		return lines(m.screen.Draw(), m.viewHeight())

	case "files":
		with, _ := m.peer()
		return "\n " + dimStyle.Render("a place to send files.") + "\n\n " +
			faintStyle.Render("drop to ") + kindStyle.Render(with.Name+at.Path) + faintStyle.Render(" <file>")

	case "branch":
		return "\n " + dimStyle.Render("holds other paths.") + "\n " +
			faintStyle.Render("go back and pick one under it.")

	default:
		return "\n " + dimStyle.Render("a "+kindOf(at)+" path.")
	}
}

func (m Model) chatView() string {
	var out strings.Builder

	if len(m.history) == 0 {
		out.WriteString(" " + faintStyle.Render("nothing said yet.") + "\n")
	}

	for _, msg := range m.history {
		when := faintStyle.Render(time.UnixMilli(msg.At).Format("15:04"))

		arrow := kindStyle.Render("←")
		if msg.Dir == convo.Out {
			arrow = pickStyle.Render("→")
		}
		out.WriteString(" " + when + " " + arrow + " " + msg.Body + "\n")
	}

	body := lines(out.String(), m.viewHeight()-1)
	if m.writing {
		return body + "\n " + pickStyle.Render("›") + " " + m.typing + pickStyle.Render("▏")
	}
	return body
}

// footer names the keys that do something here, so it never offers one that would do nothing.
func (m Model) footer() string {
	type hint struct{ key, does string }

	var keys []hint
	switch {
	case m.writing:
		keys = []hint{{"enter", "send"}, {"esc", "cancel"}}

	case m.at != levelOpen && m.list.FilterState() == list.Filtering:
		keys = []hint{{"enter", "keep"}, {"esc", "clear"}}

	case m.at == levelDevices:
		keys = []hint{{"↑↓", "move"}, {"enter", "open"}, {"/", "find"}, {"r", "reload"}, {"q", "quit"}}

	case m.at == levelPaths:
		keys = []hint{{"↑↓", "move"}, {"enter", "open"}, {"esc", "devices"}, {"r", "reload"}}

	default:
		keys = []hint{{"esc", "back"}}
		if at, ok := m.path(); ok && kindOf(at) == "chat" {
			keys = append([]hint{{"i", "write"}}, keys...)
		}
	}

	var parts []string
	for _, k := range keys {
		parts = append(parts, keyStyle.Render(k.key)+sayStyle.Render(" "+k.does))
	}
	return " " + strings.Join(parts, sayStyle.Render("  ·  "))
}
