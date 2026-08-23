package term

import "fmt"

// Run is a stretch of characters on one row that share a style. Sending runs rather than cells is
// what keeps a frame small: a line of plain text is one run, not eighty.
type Run struct {
	Text string `json:"t"`
	FG   string `json:"f,omitempty"`
	BG   string `json:"b,omitempty"`
	Bold bool   `json:"o,omitempty"`
	Dim  bool   `json:"d,omitempty"`
	Ital bool   `json:"i,omitempty"`
	Und  bool   `json:"u,omitempty"`
}

// Frame is what a watcher has to be told to catch up.
type Frame struct {
	Cols   int           `json:"cols,omitempty"`
	Rows   int           `json:"rows,omitempty"`
	Lines  map[int][]Run `json:"lines,omitempty"`
	Cursor []int         `json:"cursor,omitempty"`
}

// Empty reports whether there is nothing worth sending.
func (f Frame) Empty() bool { return f.Cols == 0 && len(f.Lines) == 0 && f.Cursor == nil }

// Painter turns a screen into frames, remembering what a watcher has already been shown so each
// frame carries only what changed.
type Painter struct {
	last       [][]Cell
	cols, rows int
	cx, cy     int
	started    bool
}

// NewPainter starts one that has shown nothing yet.
func NewPainter() *Painter { return &Painter{} }

// Frame is what changed since the last call.
//
// The first call, and any call after a resize, describes the whole screen: a watcher that has just
// arrived has nothing to diff against.
func (p *Painter) Frame(s *Screen) Frame {
	cols, rows := s.Size()
	whole := !p.started || cols != p.cols || rows != p.rows

	var frame Frame
	if whole {
		frame.Cols, frame.Rows = cols, rows
		p.last = make([][]Cell, rows)
		p.cols, p.rows, p.started = cols, rows, true
	}

	for y := range rows {
		row := s.Row(y)
		if !whole && sameRow(p.last[y], row) {
			continue
		}
		drawn := runs(row)
		p.last[y] = append(p.last[y][:0:0], row...)

		// A row that is blank on a whole-screen frame is left out: the watcher builds empty
		// rows anyway. On a diff it must be sent, because becoming empty is the change.
		if whole && drawn == nil {
			continue
		}

		if frame.Lines == nil {
			frame.Lines = make(map[int][]Run)
		}
		frame.Lines[y] = drawn
	}

	if x, y := s.Cursor(); whole || x != p.cx || y != p.cy {
		p.cx, p.cy = x, y
		frame.Cursor = []int{x, y}
	}
	return frame
}

func sameRow(a, b []Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// runs collapses a row into stretches sharing a style, with the trailing blanks dropped so a mostly
// empty screen costs almost nothing to send.
func runs(row []Cell) []Run {
	end := len(row)
	for end > 0 && row[end-1] == blank() {
		end--
	}
	if end == 0 {
		return nil
	}

	var out []Run
	start := 0
	for i := 1; i <= end; i++ {
		if i == end || row[i].Style != row[start].Style {
			out = append(out, run(row[start:i]))
			start = i
		}
	}
	return out
}

func run(cells []Cell) Run {
	text := make([]rune, len(cells))
	for i, c := range cells {
		text[i] = c.Ch
	}

	style := cells[0].Style
	r := Run{
		Text: string(text),
		Bold: style.Bold,
		Dim:  style.Dim,
		Ital: style.Italic,
		Und:  style.Underline,
	}

	fg, bg := style.FG, style.BG
	if style.Inverse {
		fg, bg = bg, fg
		// Inverting the default pair has to name both, or the swap shows as no change at all.
		if fg.Kind == ColorDefault {
			r.FG = "var(--term-bg)"
		}
		if bg.Kind == ColorDefault {
			r.BG = "var(--term-fg)"
		}
	}
	if fg.Kind != ColorDefault {
		r.FG = css(fg)
	}
	if bg.Kind != ColorDefault {
		r.BG = css(bg)
	}
	return r
}

// css names a colour the way the page will use it.
//
// It is resolved here rather than in the browser so that what arrives is already one of a fixed set
// of shapes, and the page never has to turn a number from another machine into a style.
func css(c Color) string {
	switch c.Kind {
	case ColorRGB:
		return fmt.Sprintf("rgb(%d,%d,%d)", c.R, c.G, c.B)
	case ColorIndexed:
		if c.N < 16 {
			return fmt.Sprintf("var(--t%d)", c.N)
		}
		r, g, b := palette(c.N)
		return fmt.Sprintf("rgb(%d,%d,%d)", r, g, b)
	}
	return ""
}

// palette resolves the 240 colours above the named sixteen: a 6×6×6 cube, then a grey ramp.
func palette(n uint8) (uint8, uint8, uint8) {
	if n >= 232 {
		grey := 8 + 10*(n-232)
		return grey, grey, grey
	}

	n -= 16
	steps := [6]uint8{0, 95, 135, 175, 215, 255}
	return steps[n/36], steps[(n/6)%6], steps[n%6]
}
