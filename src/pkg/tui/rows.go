package tui

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/proto"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bresilla/drop/src/pkg/book"
)

// Three lines per item: what it is, what is known about it, and where it goes. One line would fit
// more on screen, but the things worth knowing about a device do not fit on one line, and putting
// them in a second column instead is what forces a layout to be read left to right rather than down.
const rowHeight = 3

// deviceItem is one paired device.
// dividerItem separates the machines that are yours from everybody else's.
type dividerItem struct{ label string }

func (d dividerItem) FilterValue() string { return "" }

// divider draws a rule with a word in it, over the height every other row takes.
func divider(it dividerItem, width, _ int, _ bool) string {
	blank := lipgloss.NewStyle().Width(width).Render("")

	name := " " + strings.ToUpper(it.label) + " "
	rule := width - lipgloss.Width(name) - 2
	if rule < 2 {
		return blank + "\n" + lipgloss.NewStyle().Foreground(muted).Render(name) + "\n" + blank
	}

	left := rule / 2
	dashes := func(n int) string { return strings.Repeat("╌", n) }

	line := lipgloss.NewStyle().Foreground(surface).Render(dashes(left)) +
		lipgloss.NewStyle().Foreground(muted).Bold(true).Render(name) +
		lipgloss.NewStyle().Foreground(surface).Render(dashes(rule-left))

	return blank + "\n" + lipgloss.NewStyle().Width(width).Render(line) + "\n" + blank
}

type deviceItem struct {
	// self marks the row for this machine, which is not a peer and is not paired with itself.
	self bool
	// under marks a machine that sits beneath a person's name, and is drawn indented so that the
	// list reads as a person with machines rather than as a flat list with a heading in it.
	under bool
	// reaching marks a device a connection is being held to right now.
	reaching bool
	entry    book.Entry
	addr     string
}

func (d deviceItem) FilterValue() string { return d.entry.Name }

// pathItem is one path a device shares with us.
type pathItem struct {
	step step
	on   string
}

func (p pathItem) FilterValue() string { return p.step.at }

// rows draws whichever kind of item the list is holding.
type rows struct{}

func (rows) Height() int                         { return rowHeight }
func (rows) Spacing() int                        { return 0 }
func (rows) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d rows) Render(w io.Writer, m list.Model, index int, item list.Item) {
	width := m.Width()
	if width <= 0 {
		width = 80
	}
	selected := index == m.Index()

	switch it := item.(type) {
	case dividerItem:
		fmt.Fprint(w, divider(it, width, index, selected))
	case personItem:
		fmt.Fprint(w, person(it, width, index, selected))
	case deviceItem:
		fmt.Fprint(w, device(it, width, index, selected))
	case pathItem:
		fmt.Fprint(w, path(it, width, index, selected))
	case accessItem:
		fmt.Fprint(w, access(it, width, index, selected))
	case knockItem:
		fmt.Fprint(w, knock(it, width, index, selected))
	}
}

// stripe is the accent bar down the left of the row the cursor is on.
func stripe(base lipgloss.Style, selected bool) string {
	if selected {
		return base.Foreground(accent).Render("┃ ")
	}
	return base.Render("  ")
}

func device(it deviceItem, width, index int, selected bool) string {
	base := row(index, selected)
	fill := func(s string) string { return base.Width(width).Render(s) }
	inner := width - 2

	dot, dotColour := "●", green
	state := "paired"
	switch {
	case it.self:
		// Not a peer and not paired with itself: what this row offers is a look at what this
		// machine hands out.
		dot, dotColour, state = "◈", second, "you"
	case !it.entry.Paired():
		dot, dotColour, state = "○", muted, "not paired"
	case it.reaching:
		// Held right now, which is worth saying: everything else in this list is a device that
		// might answer, and this one is answering.
		state = "reachable"
	}

	var name lipgloss.TerminalColor = plain
	if selected {
		name = accent
	}

	// A machine of somebody's is set in from their name, so the two read as one thing.
	indent := ""
	if it.under {
		indent = "   "
		inner -= len(indent)
	}

	stateCol := 14
	first := fill(stripe(base, selected) + indent +
		cell(base, dotColour, 2, dot, false, false) +
		cell(base, name, inner-2-stateCol, it.entry.Name, false, true) +
		cell(base, subtext, stateCol, state, true, false))

	rest := fill(stripe(base, selected) + indent +
		cell(base, muted, inner, it.entry.ID.String(), false, false))

	where := it.addr
	if where == "" {
		where = "last seen address unknown"
	}
	last := fill(stripe(base, selected) + indent +
		cell(base, muted, inner, where, false, false))

	return first + "\n" + rest + "\n" + last
}

func path(it pathItem, width, index int, selected bool) string {
	base := row(index, selected)
	fill := func(s string) string { return base.Width(width).Render(s) }
	inner := width - 2

	var name lipgloss.TerminalColor = plain
	if selected {
		name = accent
	}

	// A way down reads as one: a folder glyph, how much is under it, and how to get there.
	if it.step.below > 0 && !it.step.is {
		first := fill(stripe(base, selected) +
			cell(base, second, 2, "▸", false, false) +
			cell(base, name, inner-2-16, it.step.name+"/", false, true) +
			cell(base, muted, 16, count(it.step.below), true, false))

		rest := fill(stripe(base, selected) +
			cell(base, second, 10, "under", false, false) +
			cell(base, muted, inner-10, "namespaces below this one", false, false))

		last := fill(stripe(base, selected) +
			cell(base, muted, inner, "enter to go in", false, false))

		return first + "\n" + rest + "\n" + last
	}

	kind := it.step.served.Kind.String()

	// The row standing for the path that was walked into, so a path with something under it can
	// still be opened.
	if it.step.here {
		first := fill(stripe(base, selected) +
			cell(base, second, 2, "◉", false, false) +
			cell(base, name, inner-2-16, it.step.at, false, true) +
			cell(base, green, 16, sendable(it.step.served), true, false))

		rest := fill(stripe(base, selected) +
			cell(base, second, 10, kind, false, false) +
			cell(base, muted, inner-10, "this path itself", false, false))

		last := fill(stripe(base, selected) +
			cell(base, muted, inner, "drop to "+it.on+it.step.at, false, false))

		return first + "\n" + rest + "\n" + last
	}

	send := "read only"
	sendColour := muted
	if it.step.served.Writable {
		send, sendColour = "you may send", green
	}
	if kind == "branch" {
		send, sendColour = "", muted
	}

	// Seen but not open. It is here to be asked for, and saying so is the whole point of the rung.
	if it.step.served.Locked {
		const lockCol = 16

		first := fill(stripe(base, selected) +
			cell(base, muted, 2, "⊘", false, false) +
			cell(base, name, inner-2-lockCol, it.step.name, false, true) +
			cell(base, peach, lockCol, "locked", true, false))

		rest := fill(stripe(base, selected) +
			cell(base, second, 10, kind, false, false) +
			cell(base, muted, inner-10, "visible, not shared with you", false, false))

		last := fill(stripe(base, selected) +
			cell(base, muted, inner, "press a to ask for it", false, false))

		return first + "\n" + rest + "\n" + last
	}

	shown := it.step.name
	if it.step.below > 0 {
		shown += "/  " + count(it.step.below)
	}

	sendCol := 16
	first := fill(stripe(base, selected) +
		cell(base, second, 2, glyph(kind), false, false) +
		cell(base, name, inner-2-sendCol, shown, false, true) +
		cell(base, sendColour, sendCol, send, true, false))

	rest := fill(stripe(base, selected) +
		cell(base, second, 10, kind, false, false) +
		cell(base, muted, inner-10, describe(kind), false, false))

	last := fill(stripe(base, selected) +
		cell(base, muted, inner, "drop to "+it.on+it.step.at, false, false))

	return first + "\n" + rest + "\n" + last
}

// sendable says whether the far end takes anything at a path.
func sendable(s proto.Served) string {
	if s.Writable {
		return "you may send"
	}
	return "read only"
}

// count says how much is under a way down, in words rather than a bare number.
func count(n int) string {
	if n == 1 {
		return "1 below"
	}
	return fmt.Sprintf("%d below", n)
}

// describe says what a kind of path is for, so the list is readable by someone who has not learnt
// the vocabulary yet.
func describe(kind string) string {
	switch kind {
	case "chat":
		return "messages, kept as a conversation"
	case "files":
		return "send and receive files"
	case "tty":
		return "a terminal, as it is being used"
	case "stream":
		return "output from a command, as it comes"
	case "link":
		return "open a link over there"
	case "branch":
		return "holds other paths, serves nothing"
	default:
		return ""
	}
}

// personItem is somebody, with their machines under it.
type personItem struct {
	name string
	of   int
}

func (p personItem) FilterValue() string { return p.name }

// person draws a name, over the height every other row takes.
func person(it personItem, width, index int, selected bool) string {
	base := row(index, selected)
	fill := func(s string) string { return base.Width(width).Render(s) }

	var name lipgloss.TerminalColor = plain
	if selected {
		name = accent
	}

	inner := width - 2

	machines := "one machine"
	if it.of != 1 {
		machines = fmt.Sprintf("%d machines", it.of)
	}

	stateCol := 14
	first := fill(stripe(base, selected) +
		cell(base, second, 2, "◈", false, false) +
		cell(base, name, inner-2-stateCol, it.name, false, true) +
		cell(base, subtext, stateCol, machines, true, false))

	// The name as a rule would spell it, because that is the thing you go and write once you have
	// decided somebody may reach something.
	rule := fill(stripe(base, selected) +
		cell(base, muted, inner, fmt.Sprintf("access = { %q }", it.name), false, false))

	return first + "\n" + rule + "\n" + fill("")
}

// access draws one row of the access pane: somebody, and whether they get in.
func access(it accessItem, width, index int, selected bool) string {
	base := row(index, selected)
	fill := func(s string) string { return base.Width(width).Render(s) }
	inner := width - 2

	var name lipgloss.TerminalColor = plain
	if selected {
		name = accent
	}

	mark, markColour, state := "·", muted, "not named"
	switch it.group {
	case groupAnyone:
		state = "off"
	case groupAsked:
		mark, markColour, state = "◇", peach, "waiting"
	}

	switch it.who.At {
	case Allowed:
		mark, markColour, state = "✓", green, "may reach it"
		if it.group == groupAnyone {
			state = "on"
		}
	case Refused:
		mark, markColour, state = "✗", red, "refused"
	}

	stateCol := 16
	first := fill(stripe(base, selected) +
		cell(base, markColour, 2, mark, false, false) +
		cell(base, name, inner-2-stateCol, it.who.Name, false, true) +
		cell(base, subtext, stateCol, state, true, false))

	rest := fill(stripe(base, selected) +
		cell(base, muted, inner, whatKind(it), false, false))

	return first + "\n" + rest + "\n" + fill("")
}

// whatKind says what a row names, and where the answer came from.
func whatKind(it accessItem) string {
	switch {
	case it.group == groupAsked:
		if it.want.Why != "" {
			return it.want.When + " — " + it.want.Why
		}
		return "asked " + it.want.When + ", and said nothing about why"
	case it.group == groupAnyone:
		return rung(it.who.Name)
	case it.who.InConfig:
		return "named in the config; refusable here, not removable"
	case it.who.Person && it.who.Machines == 1:
		return "a person, with one machine"
	case it.who.Person:
		return fmt.Sprintf("a person, with %d machines", it.who.Machines)
	default:
		return "one machine"
	}
}

// rung says what one of the rules that names nobody actually admits.
func rung(name string) string {
	switch name {
	case "anyone with the id":
		return "a public path: whoever learns this device's id"
	case "anyone paired":
		return "every device in the address book"
	default:
		return "knowledge rather than a key, so it spreads"
	}
}

// knockItem is a device that dialled and was refused.
type knockItem struct{ knock Knock }

func (k knockItem) FilterValue() string { return k.knock.ID }

// knock draws one, which is an id, when it tried, and what it wanted.
func knock(it knockItem, width, index int, selected bool) string {
	base := row(index, selected)
	fill := func(s string) string { return base.Width(width).Render(s) }
	inner := width - 2

	var name lipgloss.TerminalColor = plain
	if selected {
		name = accent
	}

	whenCol := 18
	first := fill(stripe(base, selected) +
		cell(base, muted, 2, "○", false, false) +
		cell(base, name, inner-2-whenCol, brief(it.knock.ID), false, true) +
		cell(base, subtext, whenCol, it.knock.At.Format("2 Jan 15:04"), true, false))

	wanted := it.knock.Asked
	if wanted == "" {
		wanted = "nothing it got as far as naming"
	}
	rest := fill(stripe(base, selected) +
		cell(base, second, 10, "wanted", false, false) +
		cell(base, muted, inner-10, wanted, false, false))

	last := fill(stripe(base, selected) +
		cell(base, muted, inner, it.knock.Why, false, false))

	return first + "\n" + rest + "\n" + last
}

// brief is an endpoint id short enough to read, since the whole of one tells nobody anything.
func brief(id string) string {
	if len(id) <= 16 {
		return id
	}
	return id[:8] + "…" + id[len(id)-8:]
}
