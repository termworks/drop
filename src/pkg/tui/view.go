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
		return dimStyle.Render("  starting…")
	}

	panes := lipgloss.JoinHorizontal(lipgloss.Top,
		box("devices", m.peersPane(), m.listWidth(), m.paneHeight(), m.focus == panePeers),
		box(m.pathsTitle(), m.pathsPane(), m.listWidth(), m.paneHeight(), m.focus == panePaths),
		box(m.viewTitle(), m.viewPane(), m.viewWidth(), m.paneHeight(), m.focus == paneView),
	)

	return m.header() + "\n" + panes + "\n" + m.footer()
}

// header names this device, because two of these open side by side are two machines.
func (m Model) header() string {
	left := brandStyle.Render("▍drop")
	if m.me.Name != "" {
		left += "  " + nameStyle.Render(m.me.Name) + faintStyle.Render("  "+m.me.ID)
	}

	right := dimStyle.Render(fmt.Sprintf("%d paired", len(m.peers)))
	if m.live {
		right = goodStyle.Render("● live") + dimStyle.Render("   ") + right
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return " " + left
	}
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

func (m Model) peersPane() string {
	if len(m.peers) == 0 {
		return stack(dimStyle, "nothing paired.", "") + "\n" + stack(faintStyle, "run", "drop pair", "on both", "devices.")
	}

	var out strings.Builder
	for i, p := range m.peers {
		on := i == m.atPeer

		dot := faintStyle.Render("○")
		if p.Paired() {
			dot = pickStyle.Render("●")
		}

		name := p.Name
		if on {
			name = pickStyle.Render(name)
		}

		out.WriteString(bar(on) + " " + dot + " " + fit(name, m.listWidth()-8) + "\n")
	}
	return out.String()
}

func (m Model) pathsTitle() string {
	if at, ok := m.peer(); ok {
		return at.Name
	}
	return "paths"
}

func (m Model) pathsPane() string {
	if m.loading {
		if at, ok := m.peer(); ok {
			return dimStyle.Render("asking " + at.Name + "…")
		}
		return dimStyle.Render("asking…")
	}
	if _, ok := m.peer(); !ok {
		return stack(faintStyle, "choose a", "device.")
	}
	if len(m.paths) == 0 {
		return stack(dimStyle, "shares nothing", "with you.")
	}

	var out strings.Builder
	for i, s := range m.paths {
		on := i == m.atPath
		kind := kindOf(s)

		path := s.Path
		if on {
			path = pickStyle.Render(path)
		}

		out.WriteString(bar(on) + " " + kindStyle.Render(glyph(kind)) + " " + fit(path, m.listWidth()-8) + "\n")
		out.WriteString("    " + faintStyle.Render(kind) + "\n")
	}
	return out.String()
}

func (m Model) viewTitle() string {
	at, ok := m.path()
	if !ok {
		return "nothing open"
	}
	return at.Path
}

func (m Model) viewPane() string {
	if m.trouble != "" {
		return badStyle.Render("✗ ") + m.trouble
	}

	at, ok := m.path()
	if !ok {
		if _, has := m.peer(); has {
			return faintStyle.Render("choose a path.")
		}
		return faintStyle.Render("choose a device.")
	}

	switch kindOf(at) {
	case "chat":
		return m.chatView()

	case "tty", "stream":
		if m.screen == nil {
			return faintStyle.Render("not watching.")
		}
		return lines(m.screen.Draw(), m.paneHeight())

	case "files":
		with, _ := m.peer()
		return dimStyle.Render("a place to send files.\n\n") +
			faintStyle.Render("drop to ") + kindStyle.Render(with.Name+at.Path) + faintStyle.Render(" <file>")

	case "branch":
		return stack(dimStyle, "holds other paths.") + "\n" + stack(faintStyle, "nothing to open here.")

	default:
		return dimStyle.Render("a " + kindOf(at) + " path.")
	}
}

func (m Model) chatView() string {
	var out strings.Builder

	if len(m.history) == 0 {
		out.WriteString(faintStyle.Render("nothing said yet.\n"))
	}

	for _, msg := range m.history {
		when := faintStyle.Render(time.UnixMilli(msg.At).Format("15:04"))

		if msg.Dir == convo.Out {
			out.WriteString(when + " " + pickStyle.Render("→") + " " + msg.Body + "\n")
			continue
		}
		out.WriteString(when + " " + kindStyle.Render("←") + " " + msg.Body + "\n")
	}

	body := lines(out.String(), m.paneHeight()-2)
	if m.writing {
		return body + "\n" + pickStyle.Render("›") + " " + m.typing + pickStyle.Render("▏")
	}
	return body
}

// footer is the keys that work here, named rather than listed: which ones are shown depends on what
// is open, so it never offers something that would do nothing.
func (m Model) footer() string {
	type hint struct{ key, does string }

	keys := []hint{{"tab", "panes"}, {"↑↓", "move"}, {"r", "reload"}, {"q", "quit"}}
	if at, ok := m.path(); ok && kindOf(at) == "chat" {
		keys = append([]hint{{"i", "write"}}, keys...)
	}
	if m.writing {
		keys = []hint{{"enter", "send"}, {"esc", "cancel"}}
	}

	var parts []string
	for _, k := range keys {
		parts = append(parts, keyStyle.Render(k.key)+sayStyle.Render(" "+k.does))
	}
	return " " + strings.Join(parts, sayStyle.Render("  ·  "))
}
