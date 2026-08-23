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

	if m.joining {
		return m.header() + "\n" + m.joiningView() + "\n" + m.footer()
	}

	if m.linking != nil {
		return m.header() + "\n" + m.pairingView() + "\n" + m.footer()
	}

	body := m.list.View()
	if m.at == levelDevices && len(m.peers) == 0 {
		body = m.nothingPaired()
	}
	if m.at == levelOpen {
		body = m.openView()
	}

	// Trouble at a list level has nowhere else to go: without this a mistyped ticket is
	// indistinguishable from a key that did nothing.
	if m.trouble != "" && m.at != levelOpen {
		body += "\n\n " + badStyle.Render("✗ ") + m.trouble
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

	case m.linking != nil:
		keys = []hint{{"esc", "cancel"}}

	case m.at == levelDevices:
		keys = []hint{{"p", "show code"}, {"t", "take code"}, {"↑↓", "move"}, {"enter", "open"}, {"r", "reload"}, {"q", "quit"}}

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

// nothingPaired is the first thing anyone sees, so it says what to do rather than "no items".
func (m Model) nothingPaired() string {
	return "\n " + dimStyle.Render("No devices yet.") +
		"\n\n " + keyStyle.Render("p") + faintStyle.Render(" shows a code for another device to scan or type in.") +
		"\n " + keyStyle.Render("t") + faintStyle.Render(" takes a code another device is showing.")
}

// joiningView is the other half of pairing: somewhere to put a ticket a device is showing.
//
// The field wraps rather than scrolls. A ticket is a hundred characters of identity and
// every one of them has to be checkable by eye against what the other screen says.
func (m Model) joiningView() string {
	var out strings.Builder

	out.WriteString(" " + brandStyle.Render("Enter a ticket") + "\n")
	out.WriteString("\n " + faintStyle.Render("Paste what the other device is showing.") + "\n")

	typed := m.typing
	if typed == "" {
		out.WriteString("\n " + dimStyle.Render("waiting for a ticket…") + "\n")
	} else {
		out.WriteString("\n")
		for _, at := range fold(typed, m.width-2) {
			out.WriteString(" " + kindStyle.Render(at) + "\n")
		}
	}

	out.WriteString("\n " + faintStyle.Render("press ") + keyStyle.Render("enter") + faintStyle.Render(" to pair, ") + keyStyle.Render("esc") + faintStyle.Render(" to go back"))

	if m.trouble != "" {
		out.WriteString("\n\n " + badStyle.Render(m.trouble))
	}
	return out.String()
}

// pairingView is the code, the ticket, and the wait.
//
// The code is only drawn when there is room for the whole of it. A code with its top row cut
// off is not a smaller code, it is an unreadable one, and the ticket underneath still works.
func (m Model) pairingView() string {
	var out strings.Builder

	out.WriteString(" " + brandStyle.Render("Pair a device") + "\n")

	// The ticket is long and every character of it matters, so it is folded rather than cut.
	folded := fold("drop pair "+m.linking.ticket, m.width-2)

	// What the rest of the screen costs: the title, the two lines around the ticket, the wait,
	// and a blank line either side of the code.
	spare := m.listHeight() - len(folded) - 5

	if drawn := strings.Split(strings.TrimRight(m.linking.code, "\n"), "\n"); m.linking.code != "" {
		if len(drawn) <= spare {
			out.WriteString("\n" + m.linking.code)
		} else {
			out.WriteString("\n " + faintStyle.Render("(the window is too short to draw the code)") + "\n")
		}
	}

	out.WriteString("\n " + faintStyle.Render("or run this on the other device:") + "\n")
	for _, at := range folded {
		out.WriteString(" " + kindStyle.Render(at) + "\n")
	}

	out.WriteString("\n " + goodStyle.Render("waiting for it to answer…"))

	return out.String()
}

// fold breaks a long line into ones that fit, because a ticket with its tail cut off is a
// ticket nobody can use.
func fold(text string, width int) []string {
	if width < 8 {
		width = 8
	}

	var out []string
	for len(text) > width {
		out = append(out, text[:width])
		text = text[width:]
	}
	return append(out, text)
}
