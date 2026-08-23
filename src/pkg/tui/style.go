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
var (
	rowBg    = lipgloss.AdaptiveColor{Light: "#faf9fb", Dark: "#17141d"}
	rowBgAlt = lipgloss.AdaptiveColor{Light: "#f3f0f6", Dark: "#1c1924"}
	rowBgOn  = lipgloss.AdaptiveColor{Light: "#e9e2f3", Dark: "#271f33"}
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
