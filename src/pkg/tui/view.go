package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/bresilla/drop/src/pkg/convo"
)

func (m Model) View() string {
	if m.width == 0 {
		return "starting…"
	}

	body := joinPanes(m.width,
		m.pane(panePeers, "devices", m.peersPane(), m.listWidth()),
		m.pane(panePaths, "shared with you", m.pathsPane(), m.listWidth()),
		m.pane(paneView, m.viewTitle(), m.viewPane(), m.viewWidth()),
	)

	return body + "\n" + m.footer()
}

// pane wraps a column, bordered in the accent when the keys reach it.
func (m Model) pane(which pane, title, body string, width int) string {
	style := paneStyle
	if m.focus == which {
		style = activePaneStyle
	}

	head := labelStyle.Render(title)
	if m.focus == which {
		head = titleStyle.Render(title)
	}

	return style.Width(width).Height(m.viewHeight()).Render(head + "\n\n" + body)
}

func (m Model) peersPane() string {
	if len(m.peers) == 0 {
		return dimStyle.Render("nothing paired yet.\n\nrun `drop pair`\non both devices.")
	}

	var out strings.Builder
	for i, p := range m.peers {
		mark := "  "
		name := p.Name
		if i == m.atPeer {
			mark, name = "▸ ", selectedStyle.Render(p.Name)
		}
		out.WriteString(mark + name + "\n")
		if !p.Paired() {
			out.WriteString(dimStyle.Render("  not paired") + "\n")
		}
	}
	return out.String()
}

func (m Model) pathsPane() string {
	if m.loading {
		return dimStyle.Render("asking…")
	}
	if at, ok := m.peer(); ok && len(m.paths) == 0 {
		return dimStyle.Render(at.Name + "\nshares nothing\nwith you.")
	}

	var out strings.Builder
	for i, s := range m.paths {
		mark, path := "  ", s.Path
		if i == m.atPath {
			mark, path = "▸ ", selectedStyle.Render(s.Path)
		}
		out.WriteString(mark + path + "\n")
		out.WriteString("  " + kindStyle.Render(kindOf(s)) + "\n")
	}
	return out.String()
}

func (m Model) viewTitle() string {
	at, ok := m.path()
	if !ok {
		return "nothing open"
	}
	if m.live {
		return at.Path + " · live"
	}
	return at.Path
}

func (m Model) viewPane() string {
	if m.trouble != "" {
		return badStyle.Render(m.trouble)
	}

	at, ok := m.path()
	if !ok {
		if _, has := m.peer(); has {
			return dimStyle.Render("choose a path.")
		}
		return dimStyle.Render("choose a device.")
	}

	switch kindOf(at) {
	case "chat":
		return m.chatView()
	case "tty", "stream":
		if m.screen == nil {
			return dimStyle.Render("not watching.")
		}
		return lines(m.screen.Draw(), m.viewHeight()-3)
	case "files":
		return dimStyle.Render("a files path.\n\nsend with:\n") +
			fmt.Sprintf("  drop to %s%s <file>", m.peers[m.atPeer].Name, at.Path)
	case "branch":
		return dimStyle.Render("holds other paths.\nnothing to open here.")
	default:
		return dimStyle.Render("a " + kindOf(at) + " path.")
	}
}

func (m Model) chatView() string {
	var out strings.Builder

	if len(m.history) == 0 {
		out.WriteString(dimStyle.Render("nothing said yet.\n"))
	}
	for _, msg := range m.history {
		who := "→"
		if msg.Dir != convo.Out {
			who = "←"
		}
		when := time.UnixMilli(msg.At).Format("15:04")
		out.WriteString(dimStyle.Render(when+" "+who) + " " + msg.Body + "\n")
	}

	if m.writing {
		out.WriteString("\n" + titleStyle.Render("› ") + m.typing + "▏")
	}
	return lines(out.String(), m.viewHeight()-3)
}

func (m Model) footer() string {
	keys := []string{"tab panes", "↑↓ move", "q quit"}
	if at, ok := m.path(); ok && kindOf(at) == "chat" {
		keys = append([]string{"i write"}, keys...)
	}
	if m.writing {
		keys = []string{"enter send", "esc cancel"}
	}

	left := helpStyle.Render(strings.Join(keys, " · "))

	if at, ok := m.peer(); ok {
		right := helpStyle.Render(shortID(at) + "…")
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
		if gap > 0 {
			return " " + left + strings.Repeat(" ", gap) + right
		}
	}
	return " " + left
}
