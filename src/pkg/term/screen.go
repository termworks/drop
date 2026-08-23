// Package term keeps a terminal screen: the grid another device is drawing on, rebuilt from the
// bytes it sends.
//
// This is not a terminal emulator. It never runs a program and never sends a key. It implements
// what output actually uses — a grid, a cursor, erase, scroll and colour — so a watcher can be
// shown what the far end looks like right now rather than a transcript of how it got there.
//
// Keeping the screen here rather than in the page means the parsing is testable, a late watcher
// can be handed the current screen instead of a replay, and the browser is left with nothing to
// interpret.
package term

import (
	"unicode/utf8"
)

// Kinds of colour a cell can carry.
const (
	ColorDefault uint8 = iota
	ColorIndexed
	ColorRGB
)

// Color is a cell's foreground or background.
type Color struct {
	Kind    uint8
	N       uint8
	R, G, B uint8
}

// Indexed is one of the sixteen named colours, or one of the 256 the palette extends to.
func Indexed(n uint8) Color { return Color{Kind: ColorIndexed, N: n} }

// RGB is a colour named exactly.
func RGB(r, g, b uint8) Color { return Color{Kind: ColorRGB, R: r, G: g, B: b} }

// Style is everything about a cell except which character it holds.
type Style struct {
	FG, BG    Color
	Bold      bool
	Dim       bool
	Italic    bool
	Underline bool
	Inverse   bool
}

// Cell is one position on the grid.
type Cell struct {
	Ch rune
	Style
}

func blank() Cell { return Cell{Ch: ' '} }

// Screen is the grid, the cursor, and the style the next character will take.
type Screen struct {
	cols, rows int
	grid       [][]Cell
	x, y       int

	cur   Style
	saved *savedCursor

	// pending holds bytes that ended mid-sequence, so an escape or a multi-byte character split
	// across two arrivals is finished by the next one rather than printed as rubbish.
	pending []byte
}

type savedCursor struct {
	x, y  int
	style Style
}

// New builds a screen of the given size.
func New(cols, rows int) *Screen {
	s := &Screen{}
	s.Resize(cols, rows)
	return s
}

// Size is the grid's dimensions.
func (s *Screen) Size() (cols, rows int) { return s.cols, s.rows }

// Cursor is where the next character would land.
func (s *Screen) Cursor() (x, y int) { return s.x, s.y }

// Row hands back one row of the grid. The slice is the screen's own, so callers read and do not
// keep it.
func (s *Screen) Row(y int) []Cell {
	if y < 0 || y >= s.rows {
		return nil
	}
	return s.grid[y]
}

// Resize starts a grid of a new size. The contents do not survive: reflowing a terminal is a
// different problem, and the far end redraws after a resize anyway.
func (s *Screen) Resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}

	s.cols, s.rows = cols, rows
	s.grid = make([][]Cell, rows)
	for y := range s.grid {
		s.grid[y] = blankRow(cols)
	}
	s.x, s.y = 0, 0
	s.saved = nil
	s.pending = nil
	s.cur = Style{}
}

func blankRow(cols int) []Cell {
	row := make([]Cell, cols)
	for i := range row {
		row[i] = blank()
	}
	return row
}

// Clear empties the grid and sends the cursor home.
func (s *Screen) Clear() {
	for y := range s.grid {
		s.grid[y] = blankRow(s.cols)
	}
	s.x, s.y = 0, 0
}

func (s *Screen) scroll() {
	copy(s.grid, s.grid[1:])
	s.grid[s.rows-1] = blankRow(s.cols)
}

func (s *Screen) newline() {
	s.y++
	if s.y >= s.rows {
		s.y = s.rows - 1
		s.scroll()
	}
}

func (s *Screen) put(ch rune) {
	if s.x >= s.cols {
		s.x = 0
		s.newline()
	}
	s.grid[s.y][s.x] = Cell{Ch: ch, Style: s.cur}
	s.x++
}

// Write feeds output from the far end into the screen.
func (s *Screen) Write(p []byte) (int, error) {
	n := len(p)

	buf := p
	if len(s.pending) > 0 {
		buf = append(s.pending, p...)
		s.pending = nil
	}

	i := 0
	for i < len(buf) {
		if buf[i] == 0x1b {
			used, ok := s.escape(buf[i:])
			if !ok {
				s.pending = append([]byte(nil), buf[i:]...)
				return n, nil
			}
			i += used
			continue
		}

		// A character can be split across arrivals just as an escape can.
		if !utf8.FullRune(buf[i:]) {
			s.pending = append([]byte(nil), buf[i:]...)
			return n, nil
		}

		ch, size := utf8.DecodeRune(buf[i:])
		i += size

		switch ch {
		case '\n':
			s.newline()
		case '\r':
			s.x = 0
		case '\b':
			if s.x > 0 {
				s.x--
			}
		case '\t':
			s.x = min(s.cols-1, (s.x+8)&^7)
		case 0x07:
			// A bell has nothing to draw.
		case 0x0c:
			s.Clear()
		default:
			if ch >= ' ' && ch != 0x7f {
				s.put(ch)
			}
		}
	}
	return n, nil
}

// escape consumes one sequence, reporting how many bytes it took. ok is false when the buffer
// ended before the sequence did.
func (s *Screen) escape(buf []byte) (int, bool) {
	if len(buf) < 2 {
		return 0, false
	}

	switch buf[1] {
	case '[':
		i := 2
		for i < len(buf) && isParam(buf[i]) {
			i++
		}
		if i >= len(buf) {
			return 0, false
		}
		s.csi(string(buf[2:i]), buf[i])
		return i + 1, true

	case ']':
		// An operating-system command sets things like the window title. Nothing here shows one,
		// so it is consumed and dropped; leaving it would print the title onto the grid.
		for i := 2; i < len(buf); i++ {
			if buf[i] == 0x07 {
				return i + 1, true
			}
			if buf[i] == 0x1b && i+1 < len(buf) && buf[i+1] == '\\' {
				return i + 2, true
			}
		}
		return 0, false

	case '(', ')', '#':
		if len(buf) < 3 {
			return 0, false
		}
		return 3, true

	case 'M':
		if s.y > 0 {
			s.y--
		}
		return 2, true

	case '7':
		s.save()
		return 2, true

	case '8':
		s.restore()
		return 2, true

	case 'c':
		s.Clear()
		s.cur = Style{}
		return 2, true
	}
	return 2, true
}

func isParam(b byte) bool {
	return (b >= '0' && b <= '9') || b == ';' || b == '?' || b == '<' || b == '>' || b == '!'
}

func (s *Screen) save() {
	s.saved = &savedCursor{x: s.x, y: s.y, style: s.cur}
}

func (s *Screen) restore() {
	if s.saved == nil {
		return
	}
	s.x, s.y, s.cur = s.saved.x, s.saved.y, s.saved.style
}
