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
	head, foot, said := m.header(), m.footer(), m.notice()

	room := m.bodyHeight()
	middle := lipgloss.NewStyle().Height(room).MaxHeight(room).Render(body)

	out := head + "\n" + middle
	if said != "" {
		out += "\n" + said
	}
	return out + "\n" + foot
}

// bodyHeight is the room a screen has: everything the header, the footer and any notice leave.
func (m Model) bodyHeight() int {
	room := m.height - 3
	if m.notice() != "" {
		room--
	}
	if room < 1 {
		room = 1
	}
	return room
}

// notice is the one line above the keys, for whatever went wrong or just went right.
//
// A line of its own rather than something appended to the screen: a list fills the height it is
// given, so anything written after it falls off the bottom and is never seen.
func (m Model) notice() string {
	switch {
	case m.trouble != "":
		return " " + badStyle.Render("✗ ") + fit(m.trouble, m.width-4)
	case m.said != "":
		return " " + goodStyle.Render("✓ ") + fit(m.said, m.width-4)
	}
	return ""
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

	// The toolkit's own words for an empty list say nothing about why it is empty.
	if len(m.list.Items()) == 0 {
		shown = m.emptyList()
	}

	return panel(m.listTitle(), m.width, m.bodyHeight(), shown)
}

// emptyList says why there is nothing to show, which is never the same reason twice.
func (m Model) emptyList() string {
	switch {
	case m.loading:
		return faintStyle.Render("asking…")
	case m.at == levelPaths:
		return dimStyle.Render("this device shares nothing with you.") + "\n\n" +
			faintStyle.Render("what appears here was decided over there, not here.")
	case m.list.FilterState() != 0:
		return faintStyle.Render("nothing matches.")
	}
	return faintStyle.Render("nothing here.")
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

	// What this device is doing, then what it is called. Reachability first, because it is the
	// thing that changes: the name never does.
	right := m.reach() + "   " + faintStyle.Render(m.me.Name)
	switch {
	case m.live:
		right = goodStyle.Render("● live") + "   " + faintStyle.Render(m.me.Name)
	case m.loading:
		right = peachStyle.Render("◐ asking") + "   " + faintStyle.Render(m.me.Name)
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
// reach says whether this device can be reached, and by whose doing.
//
// A daemon keeps answering after the interface is closed. This process answering means the address
// is only up while somebody is looking at it, which is worth knowing before walking away.
func (m Model) reach() string {
	switch m.me.Reach {
	case ReachDaemon:
		return kindStyle.Render("◆ daemon")
	default:
		if m.me.ID == "" {
			return faintStyle.Render("○ starting")
		}
		return goodStyle.Render("● serving")
	}
}

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

	return panel(title, m.width, m.bodyHeight(), m.inside(at))
}

// inside is what the open path shows, without the box around it.
func (m Model) inside(at proto.Served) string {
	switch kindOf(at) {
	case "chat":
		return m.chatView()

	case "tty", "stream":
		if m.screen == nil {
			return faintStyle.Render("not watching.")
		}
		return m.canvas(lines(m.screen.Draw(), m.viewHeight()))

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

	if len(before) == 0 {
		out.WriteString(dimStyle.Render("nothing here yet.") + "\n")
	}
	for _, line := range before {
		out.WriteString(line + "\n")
	}

	out.WriteString("\n" + faintStyle.Render("press ") + keyStyle.Render("s") + faintStyle.Render(" to send "+what))
	return out.String()
}

// transfers is what has changed hands on this conversation, newest last.
//
// Two lines each, the way every other list here is: the name is what you look for, and when it
// happened and how big it was are what you look at once you have found it.
func (m Model) transfers() []string {
	var out []string

	for i, at := range m.history {
		if at.Kind != convo.KindFile {
			continue
		}
		out = append(out, m.item(i, at.Dir == convo.In, at.Body, at.Extra, at.At)...)
	}
	return lastOf(out, m.viewHeight()-4)
}

// links is what has been sent to a link path, newest last.
func (m Model) links() []string {
	var out []string

	for i, at := range m.history {
		if at.Kind != convo.KindLink {
			continue
		}
		out = append(out, m.item(i, at.Dir == convo.In, at.Body, "", at.At)...)
	}
	return lastOf(out, m.viewHeight()-4)
}

// item is one thing that changed hands, as the two lines it occupies.
func (m Model) item(index int, incoming bool, name, about string, at int64) []string {
	width := m.viewWidth()
	base := row(index, false)

	arrow := "→"
	var colour lipgloss.TerminalColor = plain
	if incoming {
		arrow, colour = "←", green
	}

	// Where it went, then what it was, then when — one column each, so a list of them reads down
	// rather than across.
	first := base.Width(width).Render(
		cell(base, colour, 2, arrow, false, false) +
			cell(base, plain, width-2-8, fit(name, width-10), false, true) +
			cell(base, muted, 8, time.UnixMilli(at).Format("15:04"), true, false))

	said := about
	if said == "" {
		said = "a link"
		if !incoming {
			said = "sent from here"
		}
	}
	second := base.Width(width).Render(
		cell(base, muted, 2, "", false, false) +
			cell(base, muted, width-2, said, false, false))

	return []string{first, second}
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

// chatView is the conversation, as a conversation looks.
//
// Each message is a block rather than a line: a rule to separate it from the one before, who said
// it and when, then what they said on a ground a shade off the page. Mine on the right, theirs on
// the left, and neither running the full width — a block that reaches both edges is a paragraph,
// not a message.
func (m Model) chatView() string {
	room := m.viewHeight() - 1
	if room < 1 {
		room = 1
	}

	var said []string
	for _, msg := range m.history {
		said = append(said, m.bubble(msg)...)
	}

	if len(said) == 0 {
		said = []string{faintStyle.Render("nothing said yet.")}
	}

	// The newest sits just above what is being typed, whatever the older ones did.
	said = lastOf(said, room)
	for len(said) < room {
		said = append([]string{""}, said...)
	}

	body := strings.Join(said, "\n")
	if m.writing {
		return body + "\n" + pickStyle.Render("›") + " " + m.typing + pickStyle.Render("▏")
	}
	return body + "\n" + faintStyle.Render("press ") + keyStyle.Render("i") + faintStyle.Render(" to write")
}

// bubble draws one message: a coloured bar down the side of it, and what was said on a shaded
// ground beside the bar.
//
// The bar is what tells the two apart at a glance — theirs down the left, mine down the right —
// and it runs the height of the message so a long one still reads as one thing.
func (m Model) bubble(msg convo.Message) []string {
	width := m.viewWidth()

	// Three quarters, so the two sides cannot meet in the middle and a long message still has a
	// margin to be read against. Two of that is the bar and the space beside it.
	block := width*3/4 - 2
	if block < 12 {
		block = width - 2
	}

	mine := msg.Dir == convo.Out
	when := time.UnixMilli(msg.At).Format("15:04")

	// A blank line of the box's own colour above and below what was said, so the words sit inside
	// something rather than against its edge.
	shade := lipgloss.NewStyle().Background(saidBg).Width(block).Padding(1, 2)

	colour := second
	if mine {
		colour = accent
	}

	// Who at one end of the top line and when at the other, spaced by hand: two styled columns
	// would each be padded to their own width and the line would wrap onto a second.
	inside := block - 4
	who := m.speaker(mine)

	gap := inside - lipgloss.Width(who) - 2 - lipgloss.Width(when)
	if gap < 1 {
		gap = 1
		who = fit(who, inside-lipgloss.Width(when)-3)
	}

	// The name is a tag in the same colour as the bar, written in the terminal's own background:
	// the two together say whose message this is twice, in the same glance.
	tag := lipgloss.NewStyle().Background(colour).Foreground(sunken).Bold(true).Render(" " + who + " ")

	label := lipgloss.NewStyle().Background(saidBg).Foreground(muted)

	// The head takes the top padding and the body the bottom, so the two together are one box
	// rather than two with a seam across the middle.
	head := shade.Padding(1, 2, 0, 2).
		Render(tag + label.Render(strings.Repeat(" ", gap)) + label.Render(when))

	body := shade.Padding(0, 2, 1, 2)

	said := body.Foreground(plain).Render(msg.Body)
	switch msg.Kind {
	case convo.KindLink:
		said = body.Foreground(second).Render(msg.Body)
	case convo.KindFile:
		said = body.Foreground(plain).Render("▣ " + msg.Body + "  " + msg.Extra)
	}

	bar := lipgloss.NewStyle().Foreground(colour).Render("┃")

	var out []string
	for _, line := range strings.Split(head+"\n"+said, "\n") {
		beside := bar + " " + line
		if mine {
			beside = line + " " + bar
		}
		out = append(out, lipgloss.NewStyle().Width(width).Align(sideOf(mine)).Render(beside))
	}

	return append(out, "")
}

// sideOf is which edge a message sits against.
func sideOf(mine bool) lipgloss.Position {
	if mine {
		return lipgloss.Right
	}
	return lipgloss.Left
}

// speaker is whose message this is, by name.
func (m Model) speaker(mine bool) string {
	if mine {
		return m.me.Name
	}
	if with, ok := m.peer(); ok {
		return with.Name
	}
	return "them"
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

// canvas is what a terminal from another machine is drawn on.
//
// Black, whatever this terminal's own background is. What arrives is a screen somebody else's
// programs painted, with their own idea of what the background should be, and letting this page
// show through the gaps makes two screens out of one.
func (m Model) canvas(drawn string) string {
	ground := lipgloss.NewStyle().Background(lipgloss.Color("0")).Width(m.viewWidth())

	// The far end's own escapes reset the background to whatever this terminal calls default, so
	// the black has to be asserted again after each one. Without this the canvas is black only up
	// to the first colour the other machine chose.
	drawn = strings.ReplaceAll(drawn, "\x1b[0m", "\x1b[0m\x1b[40m")

	rows := strings.Split(drawn, "\n")
	for i, row := range rows {
		rows[i] = ground.Render(row)
	}

	// Filled to the bottom, so the canvas is a rectangle rather than a ragged edge where the far
	// end happened to stop writing.
	for len(rows) < m.viewHeight() {
		rows = append(rows, ground.Render(""))
	}
	return strings.Join(rows, "\n")
}
