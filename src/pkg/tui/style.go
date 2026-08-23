// Package tui is drop as a full-screen terminal program.
//
// Three panes that follow the model: the devices you know, the paths one of them shares with you,
// and whatever is at the path. Nothing here decides what a path does — that was declared on the far
// device, and this only draws what came back.
package tui

import "github.com/charmbracelet/lipgloss"

// The same violet the web page uses, so the two do not look like different programs. Adaptive, so a
// light terminal is not given colours picked for a dark one.
var (
	violet = lipgloss.AdaptiveColor{Light: "#5b21b6", Dark: "#a583e8"}
	plum   = lipgloss.AdaptiveColor{Light: "#7d2a72", Dark: "#c98ac0"}
	dim    = lipgloss.AdaptiveColor{Light: "#6b6376", Dark: "#8f8799"}
	line   = lipgloss.AdaptiveColor{Light: "#d9d3e0", Dark: "#332c3d"}
	bad    = lipgloss.AdaptiveColor{Light: "#a3324f", Dark: "#c76e86"}
)

var (
	titleStyle = lipgloss.NewStyle().Foreground(violet).Bold(true)
	labelStyle = lipgloss.NewStyle().Foreground(dim)
	dimStyle   = lipgloss.NewStyle().Foreground(dim)
	badStyle   = lipgloss.NewStyle().Foreground(bad)
	kindStyle  = lipgloss.NewStyle().Foreground(plum)

	// A pane that has focus is bordered in the accent, so which one the keys reach is never a guess.
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(line).
			Padding(0, 1)

	activePaneStyle = paneStyle.BorderForeground(violet)

	selectedStyle = lipgloss.NewStyle().Foreground(violet).Bold(true)

	helpStyle = lipgloss.NewStyle().Foreground(dim)
)
