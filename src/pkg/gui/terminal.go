//go:build js || android

package gui

import (
	"image/color"
	"sync"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/bresilla/drop/src/pkg/term"
)

// watching is a terminal on another device, being read here.
//
// The screen is written by whatever is reading the wire and drawn by the interface, so the lock
// lives at this boundary — term.Screen has none of its own, because everywhere else it is used by
// one goroutine at a time.
type watching struct {
	mu     sync.Mutex
	screen *term.Screen

	stop chan struct{}
	once sync.Once
}

func startWatching(from Source, peer, path string, cols, rows int, redraw func()) *watching {
	w := &watching{screen: term.New(cols, rows), stop: make(chan struct{})}

	go func() {
		_ = from.Watch(peer, path, writeTo{w, redraw}, w.resize, w.stop)
	}()
	return w
}

func (w *watching) resize(cols, rows int) {
	w.mu.Lock()
	w.screen.Resize(cols, rows)
	w.mu.Unlock()
}

func (w *watching) end() {
	w.once.Do(func() { close(w.stop) })
}

// writeTo feeds the screen and asks for a repaint, so the interface draws when there is a reason to
// rather than on a timer.
type writeTo struct {
	to     *watching
	redraw func()
}

func (t writeTo) Write(p []byte) (int, error) {
	t.to.mu.Lock()
	n, err := t.to.screen.Write(p)
	t.to.mu.Unlock()

	if t.redraw != nil {
		t.redraw()
	}
	return n, err
}

// The palette the terminal's sixteen slots map onto, chosen to sit with the rest rather than against
// it.
var slots = [16]color.NRGBA{
	{0x26, 0x21, 0x30, 0xff}, {0xb4, 0x5f, 0x7f, 0xff}, {0x7d, 0x9e, 0x77, 0xff}, {0xb1, 0x8f, 0x5e, 0xff},
	{0x6f, 0x83, 0xbd, 0xff}, {0x9a, 0x6f, 0xb5, 0xff}, {0x5f, 0x9c, 0x9c, 0xff}, {0xb3, 0xaa, 0xbd, 0xff},
	{0x4a, 0x43, 0x56, 0xff}, {0xc8, 0x7d, 0x99, 0xff}, {0x97, 0xb8, 0x8f, 0xff}, {0xc7, 0xa8, 0x77, 0xff},
	{0x8b, 0x9e, 0xd0, 0xff}, {0xb4, 0x8f, 0xc8, 0xff}, {0x7f, 0xb6, 0xba, 0xff}, {0xde, 0xd6, 0xe6, 0xff},
}

var (
	termGround = color.NRGBA{R: 0x17, G: 0x14, B: 0x1d, A: 0xff}
	termInk    = color.NRGBA{R: 0xce, G: 0xc6, B: 0xd6, A: 0xff}
)

// paint turns one cell's colour into something to draw with.
func shade(c term.Color, fallback color.NRGBA) color.NRGBA {
	switch c.Kind {
	case term.ColorIndexed:
		if c.N < 16 {
			return slots[c.N]
		}
		r, g, b := palette256(c.N)
		return color.NRGBA{R: r, G: g, B: b, A: 0xff}
	case term.ColorRGB:
		return color.NRGBA{R: c.R, G: c.G, B: c.B, A: 0xff}
	}
	return fallback
}

// palette256 resolves the 240 colours above the named sixteen: a 6×6×6 cube, then a grey ramp.
func palette256(n uint8) (uint8, uint8, uint8) {
	if n >= 232 {
		grey := 8 + 10*(n-232)
		return grey, grey, grey
	}

	n -= 16
	steps := [6]uint8{0, 95, 135, 175, 215, 255}
	return steps[n/36], steps[(n/6)%6], steps[n%6]
}

// painted is a stretch of a row sharing one style, with its colours already resolved.
//
// term.Run carries colours as the strings a stylesheet wants, which is right for the page and
// wrong here: this draws them, so it needs the values.
type painted struct {
	Text string
	FG   color.NRGBA
	Bold bool
}

// runsOf collapses a row into stretches sharing a style, with the trailing blanks dropped.
func runsOf(row []term.Cell) []painted {
	end := len(row)
	for end > 0 && row[end-1].Ch == 0x20 && row[end-1].Style == (term.Style{}) {
		end--
	}
	if end == 0 {
		return nil
	}

	var out []painted
	start := 0

	for i := 1; i <= end; i++ {
		if i == end || row[i].Style != row[start].Style {
			text := make([]rune, 0, i-start)
			for _, c := range row[start:i] {
				text = append(text, c.Ch)
			}

			style := row[start].Style
			fg := shade(style.FG, termInk)
			if style.Inverse {
				fg = shade(style.BG, termGround)
			}

			out = append(out, painted{Text: string(text), FG: fg, Bold: style.Bold})
			start = i
		}
	}
	return out
}

// terminal draws the far end's screen.
//
// A row at a time, in runs sharing a style, because a cell at a time would be eighty widgets a line
// and the grid has already worked out where each run begins and ends.
func (a *App) terminal(gtx layout.Context, w *watching) layout.Dimensions {
	w.mu.Lock()
	cols, rows := w.screen.Size()
	lines := make([][]painted, 0, rows)
	for y := range rows {
		lines = append(lines, runsOf(w.screen.Row(y)))
	}
	w.mu.Unlock()

	_ = cols

	return fill(gtx, termGround, 0, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = gtx.Constraints.Max

		return layout.UniformInset(unit.Dp(10)).Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				return a.list.Layout(gtx, len(lines), func(gtx layout.Context, y int) layout.Dimensions {
					return a.termRow(gtx, lines[y])
				})
			})
	})
}

func (a *App) termRow(gtx layout.Context, runs []painted) layout.Dimensions {
	if len(runs) == 0 {
		// An empty row still takes a line, or the screen closes up as it scrolls.
		blank := material.Body2(a.theme, " ")
		blank.Font.Typeface = "Go Mono"
		blank.TextSize = unit.Sp(13)
		return blank.Layout(gtx)
	}

	children := make([]layout.FlexChild, 0, len(runs))
	for _, run := range runs {
		children = append(children, layout.Rigid(a.termRun(run)))
	}
	return layout.Flex{}.Layout(gtx, children...)
}

func (a *App) termRun(run painted) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		text := material.Body2(a.theme, run.Text)
		text.Font.Typeface = "Go Mono"
		text.TextSize = unit.Sp(13)
		text.Color = run.FG
		text.MaxLines = 1

		if run.Bold {
			text.Font.Weight = font.Bold
		}
		return text.Layout(gtx)
	}
}
