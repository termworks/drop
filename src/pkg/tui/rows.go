package tui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Three lines per item: what it is, what is known about it, and where it goes. One line would fit
// more on screen, but the things worth knowing about a device do not fit on one line, and putting
// them in a second column instead is what forces a layout to be read left to right rather than down.
const rowHeight = 3

// deviceItem is one paired device.
type deviceItem struct {
	entry book.Entry
	addr  string
}

func (d deviceItem) FilterValue() string { return d.entry.Name }

// pathItem is one path a device shares with us.
type pathItem struct {
	served proto.Served
	on     string
}

func (p pathItem) FilterValue() string { return p.served.Path }

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
		return base.Foreground(violet).Render("┃ ")
	}
	return base.Render("  ")
}

func device(it deviceItem, width, index int, selected bool) string {
	base := row(index, selected)
	line := func(s string) string { return base.Width(width).Render(s) }
	inner := width - 2

	dot, dotColour := "●", good
	state := "paired"
	if !it.entry.Paired() {
		dot, dotColour, state = "○", faint, "not paired"
	}

	name := ink
	if selected {
		name = violet
	}

	stateCol := 14
	first := line(stripe(base, selected) +
		cell(base, dotColour, 2, dot, false, false) +
		cell(base, name, inner-2-stateCol, it.entry.Name, false, true) +
		cell(base, dim, stateCol, state, true, false))

	second := line(stripe(base, selected) +
		cell(base, faint, inner, it.entry.ID.String(), false, false))

	where := it.addr
	if where == "" {
		where = "last seen address unknown"
	}
	third := line(stripe(base, selected) +
		cell(base, faint, inner, where, false, false))

	return first + "\n" + second + "\n" + third
}

func path(it pathItem, width, index int, selected bool) string {
	base := row(index, selected)
	line := func(s string) string { return base.Width(width).Render(s) }
	inner := width - 2

	kind := it.served.Kind.String()

	name := ink
	if selected {
		name = violet
	}

	send := "read only"
	sendColour := faint
	if it.served.Writable {
		send, sendColour = "you may send", good
	}
	if kind == "branch" {
		send, sendColour = "", faint
	}

	sendCol := 16
	first := line(stripe(base, selected) +
		cell(base, plum, 2, glyph(kind), false, false) +
		cell(base, name, inner-2-sendCol, it.served.Path, false, true) +
		cell(base, sendColour, sendCol, send, true, false))

	second := line(stripe(base, selected) +
		cell(base, plum, 10, kind, false, false) +
		cell(base, faint, inner-10, describe(kind), false, false))

	third := line(stripe(base, selected) +
		cell(base, faint, inner, "drop to "+it.on+it.served.Path, false, false))

	return first + "\n" + second + "\n" + third
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
