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

// The same violet the web page uses, so the two do not look like different programs. Adaptive, so a
// light terminal is not handed colours picked for a dark one.
var (
	violet = lipgloss.AdaptiveColor{Light: "#5b21b6", Dark: "#a583e8"}
	plum   = lipgloss.AdaptiveColor{Light: "#7d2a72", Dark: "#c98ac0"}
	ink    = lipgloss.AdaptiveColor{Light: "#1f1b26", Dark: "#e4dfeb"}
	dim    = lipgloss.AdaptiveColor{Light: "#6b6376", Dark: "#8f8799"}
	faint  = lipgloss.AdaptiveColor{Light: "#a49dae", Dark: "#5d5568"}
	line   = lipgloss.AdaptiveColor{Light: "#ddd7e4", Dark: "#332c3d"}
	good   = lipgloss.AdaptiveColor{Light: "#2f6f4f", Dark: "#7fc9a2"}
	bad    = lipgloss.AdaptiveColor{Light: "#a3324f", Dark: "#c76e86"}
)

var (
	brandStyle = lipgloss.NewStyle().Foreground(violet).Bold(true)
	nameStyle  = lipgloss.NewStyle().Foreground(ink).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(dim)
	faintStyle = lipgloss.NewStyle().Foreground(faint)
	goodStyle  = lipgloss.NewStyle().Foreground(good)
	badStyle   = lipgloss.NewStyle().Foreground(bad)
	kindStyle  = lipgloss.NewStyle().Foreground(plum)
	pickStyle  = lipgloss.NewStyle().Foreground(violet).Bold(true)

	// A key is named in the accent and what it does in the quiet colour, so the footer reads as a
	// list of verbs rather than a wall of grey.
	keyStyle = lipgloss.NewStyle().Foreground(violet).Bold(true)
	sayStyle = lipgloss.NewStyle().Foreground(dim)

	badgeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(violet).Padding(0, 1)
)

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

// box draws a titled pane.
//
// Lip Gloss has no border title of its own, so the top edge is built by hand: the alternative is a
// heading floating above an untitled box, which reads as two things rather than one.
func box(title, body string, width, height int, focused bool) string {
	edge := lipgloss.NewStyle().Foreground(line)
	if focused {
		edge = lipgloss.NewStyle().Foreground(violet)
	}

	label := dimStyle.Render(title)
	if focused {
		label = brandStyle.Render(title)
	}

	// Two corners, the opening dash, and a space either side of the title.
	rest := width - lipgloss.Width(title) - 5
	if rest < 0 {
		rest = 0
	}

	top := edge.Render("╭─ ") + label + edge.Render(" "+strings.Repeat("─", rest)+"╮")
	bottom := edge.Render("╰" + strings.Repeat("─", width-2) + "╯")

	inner := lipgloss.NewStyle().Width(width - 4).Height(height).MaxHeight(height).Render(body)

	var out strings.Builder
	out.WriteString(top + "\n")
	for _, row := range strings.Split(inner, "\n") {
		pad := width - 4 - lipgloss.Width(row)
		if pad < 0 {
			pad = 0
		}
		out.WriteString(edge.Render("│") + " " + row + strings.Repeat(" ", pad) + " " + edge.Render("│") + "\n")
	}
	out.WriteString(bottom)

	return out.String()
}

// bar is the accent stripe that marks the row the cursor is on.
func bar(on bool) string {
	if on {
		return pickStyle.Render("▌")
	}
	return " "
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

// stack styles each line on its own, then joins them.
//
// A style applied across a newline pads and aligns every line to the widest, which turns a short
// second line into an indented one.
func stack(style lipgloss.Style, lines ...string) string {
	out := make([]string, 0, len(lines))
	for _, at := range lines {
		out = append(out, style.Render(at))
	}
	return strings.Join(out, "\n")
}
