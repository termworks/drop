package tui

import (
	"strings"

	"github.com/bresilla/drop/src/pkg/proto"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"

	"github.com/bresilla/drop/src/pkg/convo"
)

func (m Model) View() string {
	if m.width == 0 {
		return dimStyle.Render("  starting…")
	}

	return m.frame(m.body())
}

// frame is the shape of every screen: a line saying where you are, the screen itself, and the keys
// along the bottom.
//
// The body is given exactly the room that is left and no more, so the keys sit on the last line of
// the terminal rather than wherever the content happened to stop. A footer that floats halfway up
// the screen reads as part of the content.
func (m Model) frame(body string) string {
	head, foot := m.header(), m.footer()

	room := m.height - lipgloss.Height(head) - lipgloss.Height(foot)
	if room < 1 {
		room = 1
	}

	middle := lipgloss.NewStyle().Height(room).MaxHeight(room).Render(body)

	return head + "\n" + middle + "\n" + foot
}

// body is whatever screen is open.
func (m Model) body() string {
	switch {
	case m.joining:
		return m.joiningView()

	case m.linking != nil:
		return m.pairingView()

	case m.at == levelOpen:
		return m.openView()

	case m.at == levelDevices && len(m.peers) == 0:
		return m.nothingPaired()
	}

	shown := m.list.View()

	// Trouble at a list level has nowhere else to go: without this a mistyped ticket is
	// indistinguishable from a key that did nothing.
	if m.trouble != "" {
		shown += "\n\n" + badStyle.Render("✗ ") + m.trouble
	}

	return panel(m.listTitle(), m.width, m.height-3, shown)
}

// listTitle names what is being listed, in the panel's top edge.
func (m Model) listTitle() string {
	if m.at == levelPaths {
		if with, ok := m.peer(); ok {
			return with.Name + " shares"
		}
		return "shares"
	}
	return "devices"
}

// header is where you are and which device you are.
func (m Model) header() string {
	left := brandStyle.Render("drop")
	if trail := m.where(); trail != "" {
		left += faintStyle.Render("  ›  ") + trail
	}

	right := badge(true, m.me.Name, m.me.Name)
	switch {
	case m.live:
		right = goodStyle.Render("● live") + "   " + faintStyle.Render(m.me.Name)
	case m.loading:
		right = peachStyle.Render("◐ asking") + "   " + faintStyle.Render(m.me.Name)
	default:
		right = faintStyle.Render(m.me.Name)
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return " " + fit(left, m.width-2)
	}

	line := " " + left + strings.Repeat(" ", gap) + right + " "

	// A rule under it, so the screen has a top edge without spending a line on a box.
	return line + "\n" + lipgloss.NewStyle().Foreground(surface).Render(strings.Repeat("─", m.width))
}

// where is the trail of what has been entered, without the program's own name: the name is already
// on the line, in the accent, and saying it twice is how a header stops being read.
func (m Model) where() string {
	var parts []string

	if m.linking != nil {
		return crumb("pairing")
	}
	if m.joining {
		return crumb("pairing")
	}
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
	at, ok := m.path()
	if !ok {
		return ""
	}

	title := at.Path
	if m.live {
		title = at.Path + " · live"
	}

	return panel(title, m.width, m.height-3, m.inside(at))
}

// inside is what the open path shows, without the box around it.
func (m Model) inside(at proto.Served) string {
	if m.trouble != "" && !m.putting && m.offering == nil {
		return badStyle.Render("✗ ") + m.trouble
	}

	switch kindOf(at) {
	case "chat":
		return m.chatView()

	case "tty", "stream":
		if m.screen == nil {
			return faintStyle.Render("not watching.")
		}
		return lines(m.screen.Draw(), m.viewHeight())

	case "files":
		return m.putView("a file", "files", m.transfers())

	case "link", "bookmark":
		return m.putView("a link", "link", m.links())

	case "branch":
		return dimStyle.Render("holds other paths.") + "\n" +
			faintStyle.Render("go back and pick one under it.")

	default:
		return dimStyle.Render("a " + kindOf(at) + " path.")
	}
}

// putView is the files and links screen: what has gone before, and the line for sending the next.
//
// The same shape for both, because they are the same act. What differs is the word for the thing
// and whether a path can be completed, and neither is worth a second screen.
func (m Model) putView(what, kind string, before []string) string {
	var out strings.Builder

	if m.putting {
		out.WriteString("\n " + brandStyle.Render("send") + " " + faintStyle.Render(what) + "\n")
		out.WriteString("\n " + kindStyle.Render(tailOf(m.typing, m.width-4)) + keyStyle.Render("▏") + "\n")

		if len(m.options) > 0 {
			out.WriteString("\n " + faintStyle.Render(strings.Join(shortly(m.options, m.width-4), "  ")) + "\n")
		}

		hint := "enter sends, esc goes back"
		if kind == "files" {
			hint = "tab completes, " + hint
		}
		out.WriteString("\n " + faintStyle.Render(hint))
		return out.String()
	}

	if m.offering != nil {
		name, done, size := m.offering.read()
		out.WriteString("\n " + goodStyle.Render("sending ") + kindStyle.Render(name) + "\n")
		out.WriteString("\n " + bar(done, size, m.width-4) + "\n")
		out.WriteString(" " + faintStyle.Render(sizeOf(done)+" of "+sizeOf(size)))
		return out.String()
	}

	if m.said != "" {
		out.WriteString("\n " + goodStyle.Render("✓ ") + dimStyle.Render(m.said) + "\n")
	}
	if m.trouble != "" {
		out.WriteString("\n " + badStyle.Render("✗ ") + m.trouble + "\n")
	}

	if len(before) == 0 {
		out.WriteString("\n " + dimStyle.Render("nothing here yet."))
	}
	for _, line := range before {
		out.WriteString("\n " + line)
	}

	out.WriteString("\n\n " + faintStyle.Render("press ") + keyStyle.Render("s") + faintStyle.Render(" to send "+what))
	return out.String()
}

// transfers is what has changed hands on this conversation, newest last.
func (m Model) transfers() []string {
	var out []string

	for _, at := range m.history {
		if at.Kind != convo.KindFile {
			continue
		}

		arrow := dimStyle.Render("→")
		if at.Dir == convo.In {
			arrow = goodStyle.Render("←")
		}
		out = append(out, arrow+" "+kindStyle.Render(at.Body)+"  "+faintStyle.Render(at.Extra))
	}
	return lastOf(out, m.viewHeight()-6)
}

// links is what has been sent to a link path, newest last.
func (m Model) links() []string {
	var out []string

	for _, at := range m.history {
		if at.Kind != convo.KindLink {
			continue
		}

		arrow := dimStyle.Render("→")
		if at.Dir == convo.In {
			arrow = goodStyle.Render("←")
		}
		out = append(out, arrow+" "+kindStyle.Render(at.Body))
	}
	return lastOf(out, m.viewHeight()-6)
}

// bar draws how far along a transfer is. A size nobody knows yet gets a moving mark rather than a
// bar that would have to lie about a fraction.
func bar(done, size int64, width int) string {
	if width < 8 {
		width = 8
	}
	if size <= 0 {
		return faintStyle.Render(strings.Repeat("·", width))
	}

	full := int(int64(width) * done / size)
	if full > width {
		full = width
	}
	return goodStyle.Render(strings.Repeat("█", full)) + faintStyle.Render(strings.Repeat("░", width-full))
}

// shortly trims a list of completions to what fits on one line.
func shortly(names []string, width int) []string {
	var out []string
	used := 0

	for _, name := range names {
		if used+len(name)+2 > width {
			out = append(out, "…")
			break
		}
		out = append(out, name)
		used += len(name) + 2
	}
	return out
}

func lastOf(lines []string, n int) []string {
	if n < 1 || len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// tailOf keeps the end of a line that is too long to show.
//
// The end, not the beginning: what somebody is typing is at the end, and a path whose last segment
// is off the screen cannot be checked before it is sent.
func tailOf(text string, width int) string {
	if width < 8 {
		width = 8
	}

	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	return "…" + string(runes[len(runes)-width+1:])
}

// joiningView is the other half of pairing: somewhere to put a ticket a device is showing.
//
// The ticket wraps rather than scrolls. It is a hundred characters of identity and every one of
// them has to be checkable by eye against what the other screen says.
func (m Model) joiningView() string {
	var out strings.Builder

	out.WriteString(dimStyle.Render("Paste what the other device is showing.") + "\n\n")

	switch {
	case m.typing == "":
		out.WriteString(faintStyle.Render("waiting for a ticket…") + "\n")
	default:
		for _, at := range fold(m.typing, m.panelWidth()-4) {
			out.WriteString(kindStyle.Render(at) + "\n")
		}
	}

	out.WriteString("\n" + faintStyle.Render("press ") + keyStyle.Render("enter") +
		faintStyle.Render(" to pair, ") + keyStyle.Render("esc") + faintStyle.Render(" to go back"))

	if m.trouble != "" {
		out.WriteString("\n\n" + badStyle.Render("✗ ") + m.trouble)
	}

	return m.middle(panel("take a code", m.panelWidth(), 0, out.String()))
}

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
		if at, ok := m.path(); ok {
			switch {
			case kindOf(at) == "chat":
				keys = append([]hint{{"i", "write"}}, keys...)
			case putsInto(kindOf(at)):
				keys = append([]hint{{"s", "send"}}, keys...)
			}
		}
		if m.putting {
			keys = []hint{{"tab", "complete"}, {"enter", "send"}, {"esc", "cancel"}}
		}
	}

	var parts []string
	for _, k := range keys {
		parts = append(parts, chip(k.key, k.does))
	}
	return " " + strings.Join(parts, "  ")
}

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

func (m Model) chatView() string {
	var said []string

	for _, msg := range m.history {
		when := faintStyle.Render(time.UnixMilli(msg.At).Format("15:04"))

		arrow := kindStyle.Render("←")
		if msg.Dir == convo.Out {
			arrow = pickStyle.Render("→")
		}
		said = append(said, when+" "+arrow+" "+msg.Body)
	}

	if len(said) == 0 {
		said = []string{faintStyle.Render("nothing said yet.")}
	}

	// Room for the conversation: everything the panel has, less the line to write on.
	room := m.viewHeight() - 1
	said = lastOf(said, room)

	// Pushed to the bottom, because that is where a conversation is: the newest line sits just
	// above what you are typing, wherever the older ones happen to have got to.
	for len(said) < room {
		said = append([]string{""}, said...)
	}

	body := strings.Join(said, "\n")
	if m.writing {
		return body + "\n" + pickStyle.Render("›") + " " + m.typing + pickStyle.Render("▏")
	}
	return body + "\n" + faintStyle.Render("press ") + keyStyle.Render("i") + faintStyle.Render(" to write")
}

// middle puts something in the centre of the room a screen has, for the screens that are one thing
// rather than a list of them.
func (m Model) middle(what string) string {
	room := m.height - 3
	if room < 1 {
		room = 1
	}
	return lipgloss.Place(m.width, room, lipgloss.Center, lipgloss.Center, what)
}

// panelWidth is how wide a centred panel should be: most of a narrow terminal, and a readable
// column of a wide one rather than a box stretched across a monitor.
func (m Model) panelWidth() int {
	width := m.width - 8
	if width > 72 {
		width = 72
	}
	if width < 24 {
		width = 24
	}
	return width
}

// nothingPaired is the first thing anyone sees, so it says what to do rather than "no items".
func (m Model) nothingPaired() string {
	body := strings.Join([]string{
		"",
		nameStyle.Render("No devices yet"),
		"",
		dimStyle.Render("drop talks to devices you have paired with, and nothing else."),
		"",
		keyStyle.Render("p") + sayStyle.Render("  show a code for the other device to scan or type in"),
		keyStyle.Render("t") + sayStyle.Render("  take a code the other device is showing"),
		"",
		faintStyle.Render("or run ") + kindStyle.Render("drop pair") + faintStyle.Render(" on both, from a terminal"),
		"",
	}, "\n")

	return m.middle(panel("pair a device", m.panelWidth(), 0, body))
}

// pairingView is the code, the ticket, and the wait.
//
// The code is only drawn when there is room for the whole of it. A code with its top row cut off is
// not a smaller code, it is an unreadable one, and the ticket underneath still works.
func (m Model) pairingView() string {
	var out strings.Builder

	width := m.panelWidth()
	folded := fold("drop pair "+m.linking.ticket, width-4)

	// What the panel has room for: the body, less its own two edges, less the line that says what
	// to do with the code, the ticket under it, and the line saying we are waiting.
	spare := (m.height - 3) - 2 - len(folded) - 2

	drawn := strings.Split(strings.TrimRight(m.linking.code, "\n"), "\n")
	switch {
	case m.linking.code == "":
	case len(drawn) <= spare:
		out.WriteString(m.linking.code)
	default:
		// A code with its top row cut off is not a smaller code, it is an unreadable one, and the
		// ticket underneath still works.
		out.WriteString(faintStyle.Render("(the window is too short to draw the code)") + "\n")
	}

	out.WriteString(faintStyle.Render("point a camera at it, or run this over there:") + "\n")
	for _, at := range folded {
		out.WriteString(kindStyle.Render(at) + "\n")
	}
	out.WriteString(goodStyle.Render("◐ waiting for it to answer…"))

	return m.middle(panel("show a code", width, 0, out.String()))
}
