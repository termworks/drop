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

// The palette is Catppuccin Mocha, on whatever background the terminal already has.
//
// Taken rather than invented, and taken from the one the rest of these tools use, so drop does not
// arrive with its own idea of what a terminal should look like. Nothing paints a background over
// the whole screen: a terminal already has one, usually chosen on purpose.
var (
	text    = lipgloss.Color("#cdd6f4")
	subtext = lipgloss.Color("#a6adc8")
	muted   = lipgloss.Color("#6c7086")
	surface = lipgloss.Color("#313244")
	sunken  = lipgloss.Color("#242534")

	mauve = lipgloss.Color("#cba6f7")
	pink  = lipgloss.Color("#f5c2e7")
	green = lipgloss.Color("#a6e3a1")
	red   = lipgloss.Color("#f38ba8")
	peach = lipgloss.Color("#fab387")
	blue  = lipgloss.Color("#89b4fa")
)

var (
	brandStyle = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	nameStyle  = lipgloss.NewStyle().Foreground(text).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(subtext)
	faintStyle = lipgloss.NewStyle().Foreground(muted)
	goodStyle  = lipgloss.NewStyle().Foreground(green)
	badStyle   = lipgloss.NewStyle().Foreground(red)
	kindStyle  = lipgloss.NewStyle().Foreground(pink)
	pickStyle  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	sayStyle   = lipgloss.NewStyle().Foreground(muted)
	peachStyle = lipgloss.NewStyle().Foreground(peach)

	// A key named inside a sentence, as opposed to one in the footer, which gets a chip.
	keyStyle = lipgloss.NewStyle().Foreground(mauve).Bold(true)
)

// chip is one key in the footer: the key itself reversed out of the accent, then what it does.
//
// Reversed rather than merely coloured, because a footer of coloured words is a sentence nobody
// reads. A block of background says "this is a thing you press" before any of it is read.
func chip(key, does string) string {
	return lipgloss.NewStyle().Foreground(sunken).Background(mauve).Bold(true).Render(" "+key+" ") +
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

// What each kind of path looks like in a list. Glyphs rather than words: the word is already on the
// line below, and a shape is quicker to scan down a column than a second string.
var glyphs = map[string]string{
	"chat":   "▤",
	"files":  "▣",
	"tty":    "▮",
	"stream": "▶",
	"link":   "◈",
	"branch": "▸",
}

func glyph(kind string) string {
	if g, ok := glyphs[kind]; ok {
		return g
	}
	return "·"
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
// Only what is selected carries one: banding every other row competes with the selection, and the
// terminal's own background is what the rest should be.
var rowBgOn = surface

// row is the background a line of an item is drawn on.
func row(_ int, selected bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().Background(rowBgOn)
	}
	return lipgloss.NewStyle()
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
