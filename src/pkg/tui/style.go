// Package tui is drop as a full-screen terminal program.
//
// Three panes that follow the model: the devices you know, the paths one of them shares with you,
// and whatever is at the path. Nothing here decides what a path does — that was declared on the far
// device, and this only draws what came back.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The palette is the terminal's own sixteen colours, and nothing else.
//
// Not a scheme of its own. Whoever is reading this has already chosen what red and grey mean on
// their screen — in a theme, over ssh, in whatever terminal they happen to be sitting at — and a
// program that ships its own idea of violet is a program that looks wrong everywhere except the
// machine it was written on. Sixteen colours also survive a terminal that has no more than that.
//
// Plain text has no colour at all: it is the terminal's foreground, whatever that is.
var (
	plain   = lipgloss.NoColor{}  // whatever the terminal writes in
	subtext = lipgloss.Color("7") // white
	muted   = lipgloss.Color("8") // bright black, the one grey there is
	surface = lipgloss.Color("8") // borders, and the ground under what is selected
	sunken  = lipgloss.Color("0") // black, for text on top of an accent
	accent  = lipgloss.Color("5") // magenta: this program, and the keys it answers to
	second  = lipgloss.Color("6") // cyan: paths, tickets, and things to be typed
	green   = lipgloss.Color("2") // it worked
	red     = lipgloss.Color("1") // it did not
	peach   = lipgloss.Color("3") // yellow: it is happening
)

var (
	brandStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	nameStyle  = lipgloss.NewStyle().Foreground(plain).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(subtext)
	faintStyle = lipgloss.NewStyle().Foreground(muted)
	goodStyle  = lipgloss.NewStyle().Foreground(green)
	badStyle   = lipgloss.NewStyle().Foreground(red)
	kindStyle  = lipgloss.NewStyle().Foreground(second)
	pickStyle  = lipgloss.NewStyle().Foreground(accent).Bold(true)
	sayStyle   = lipgloss.NewStyle().Foreground(muted)
	peachStyle = lipgloss.NewStyle().Foreground(peach)

	// A key named inside a sentence, as opposed to one in the footer, which gets a chip.
	keyStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
)

// chip is one key in the footer: the key itself reversed out of the accent, then what it does.
//
// Reversed rather than merely coloured, because a footer of coloured words is a sentence nobody
// reads. A block of background says "this is a thing you press" before any of it is read.
func chip(key, does string) string {
	return lipgloss.NewStyle().Foreground(sunken).Background(accent).Bold(true).Render(" "+key+" ") +
		sayStyle.Render(" "+does)
}

// badge is a state, as a dot and a word: filled when it is on, hollow when it is not.
func badge(on bool, yes, no string) string {
	if on {
		return goodStyle.Render("● " + yes)
	}
	return faintStyle.Render("○ " + no)
}

// panel is a rounded box with its name written into the top edge, which is how every pane here is
// separated from the next without a line of its own.
func panel(title string, width, height int, body string) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(surface).
		Padding(0, 1)

	if width > 2 {
		box = box.Width(width - 2)
	}
	if height > 2 {
		box = box.Height(height - 2)
	}
	if title != "" {
		box = box.BorderTop(true)
	}
	return withTitle(box.Render(body), title)
}

// withTitle writes a name into the top border of an already-drawn box.
//
// Lip Gloss has no titled border, and drawing the box by hand to get one would mean owning every
// corner and join. Overwriting the top edge keeps the box the toolkit's problem.
func withTitle(box, title string) string {
	if title == "" {
		return box
	}

	lines := strings.Split(box, "\n")
	if len(lines) == 0 {
		return box
	}

	edge := lipgloss.NewStyle().Foreground(surface)
	name := " " + brandStyle.Render(title) + " "
	corner := edge.Render("╭─")

	// What is left of the top edge once the corner, the name and the far corner have had theirs.
	rest := lipgloss.Width(lines[0]) - lipgloss.Width(corner) - lipgloss.Width(name) - 1
	if rest < 0 {
		return box
	}
	lines[0] = corner + name + edge.Render(strings.Repeat("─", rest)+"╮")

	return strings.Join(lines, "\n")
}

// fit shortens text to a width, keeping the end.
//
// The end is what tells two paths apart — /friends/chat and /friends/files share everything but
// their last segment — so the head is what gives way.
func fit(text string, width int) string {
	if width < 2 || lipgloss.Width(text) <= width {
		return text
	}

	runes := []rune(text)
	keep := width - 1
	if keep < 1 {
		keep = 1
	}
	return "…" + string(runes[len(runes)-keep:])
}

// A row carries a solid background across its whole width, so three lines read as one block and
// columns line up down the list. Alternating shades separate neighbours without a rule between them.
//
// From the grayscale ramp rather than the sixteen, because this is the one place a colour must be
// a known distance from the one beside it: bright black is wherever a theme put it, and next to a
// dark background it can be lighter than the text.
var (
	rowBg    = lipgloss.Color("232")
	rowBgAlt = lipgloss.Color("235")
	rowBgOn  = lipgloss.Color("237")

	// A message sits on something you can see the edges of, a step further from the page than a
	// list row: a box whose ground is almost the page is a box nobody can find the sides of.
	saidBg = lipgloss.Color("236")
)

// row is the background a line of an item is drawn on.
func row(index int, selected bool) lipgloss.Style {
	switch {
	case selected:
		return lipgloss.NewStyle().Background(rowBgOn)
	case index%2 == 1:
		return lipgloss.NewStyle().Background(rowBgAlt)
	default:
		return lipgloss.NewStyle().Background(rowBg)
	}
}

// cell draws text in a fixed-width column carrying the row's background, which is what keeps the
// block solid and the columns aligned across rows.
func cell(base lipgloss.Style, fg lipgloss.TerminalColor, width int, text string, right, bold bool) string {
	if width < 1 {
		width = 1
	}

	style := base.Foreground(fg).Width(width).Bold(bold)
	if right {
		style = style.Align(lipgloss.Right)
	}
	return style.Render(clip(text, width))
}

// clip cuts text to a width, from the end, marking that it was cut.
func clip(text string, width int) string {
	if width < 1 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}

	runes := []rune(text)
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

// crumb is where you are, as a path through what you entered.
func crumb(parts ...string) string {
	shown := make([]string, 0, len(parts))
	for i, at := range parts {
		if at == "" {
			continue
		}
		if i == len(parts)-1 {
			shown = append(shown, nameStyle.Render(at))
			continue
		}
		shown = append(shown, dimStyle.Render(at))
	}
	return strings.Join(shown, faintStyle.Render(" › "))
}
