package tui

import (
	"fmt"
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
type deviceItem struct {
	// self marks the row for this machine, which is not a peer and is not paired with itself.
	self  bool
	entry book.Entry
	addr  string
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
	case deviceItem:
		fmt.Fprint(w, device(it, width, index, selected))
	case pathItem:
		fmt.Fprint(w, path(it, width, index, selected))
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
	}

	var name lipgloss.TerminalColor = plain
	if selected {
		name = accent
	}

	stateCol := 14
	first := fill(stripe(base, selected) +
		cell(base, dotColour, 2, dot, false, false) +
		cell(base, name, inner-2-stateCol, it.entry.Name, false, true) +
		cell(base, subtext, stateCol, state, true, false))

	rest := fill(stripe(base, selected) +
		cell(base, muted, inner, it.entry.ID.String(), false, false))

	where := it.addr
	if where == "" {
		where = "last seen address unknown"
	}
	last := fill(stripe(base, selected) +
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

	send := "read only"
	sendColour := muted
	if it.step.served.Writable {
		send, sendColour = "you may send", green
	}
	if kind == "branch" {
		send, sendColour = "", muted
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

var _ = strings.Join
