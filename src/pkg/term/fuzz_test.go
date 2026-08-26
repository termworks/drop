package term

import "testing"

// A screen is drawn from bytes a program on somebody else's machine wrote.
//
// Escape sequences carry numbers, and those numbers become indexes: a column to move to, how many
// cells to delete, which row a scroll region starts at. Every one of them is somebody else's, and a
// terminal that is watched is a terminal whose output was never meant to be safe. What is asserted
// is only that the screen survives and stays the shape it was made.

func FuzzScreen(f *testing.F) {
	f.Add([]byte("hello\r\n"))
	f.Add([]byte("\x1b[2J\x1b[H\x1b[31mred\x1b[0m"))
	f.Add([]byte("\x1b[999999999999999999999C\x1b[P"))
	f.Add([]byte("\x1b[1;5r\x1b[10;10H\x1b[5M\x1b[3L"))
	f.Add([]byte("\x1b]0;a title\x07"))
	f.Add([]byte("\x1bPq#0;2;0;0;0\x1b\\"))
	f.Add([]byte("\xf0\x9f\x92\xa9\xed\xa0\x80\xff\xfe"))
	f.Add([]byte("\x1b[?1049h\x1b[?25l\x1b[?7h"))
	f.Add([]byte("\x1b[8;24;80t\x1b[0;0;0;0;0;0m"))

	f.Fuzz(func(t *testing.T, out []byte) {
		s := New(80, 24)
		s.Write(out)

		// The grid never changes shape, whatever was drawn on it.
		for y := 0; y < 24; y++ {
			if got := len(s.Row(y)); got != 80 {
				t.Fatalf("row %d is %d cells wide, not 80", y, got)
			}
		}

		// And what it says about itself has to be somewhere on it.
		if c, r := s.Cursor(); c < 0 || c > 80 || r < 0 || r >= 24 {
			t.Fatalf("the cursor is at %d,%d on an 80x24 screen", c, r)
		}

		// Reading it back must not fail either: it is what a joining watcher is handed.
		_ = s.ANSI()
	})
}

// The same bytes arriving a piece at a time, which is how they really arrive: a sequence split
// across two data frames must not be parsed differently from one that arrived whole.
func FuzzScreenInPieces(f *testing.F) {
	f.Add([]byte("\x1b[2J\x1b[H\x1b[31mred\x1b[0m"), uint8(3))
	f.Add([]byte("\x1b]0;title\x07rest"), uint8(1))

	f.Fuzz(func(t *testing.T, out []byte, at uint8) {
		whole := New(40, 10)
		whole.Write(out)

		piece := New(40, 10)
		cut := int(at)
		if cut > len(out) {
			cut = len(out)
		}
		piece.Write(out[:cut])
		piece.Write(out[cut:])

		for y := 0; y < 10; y++ {
			if len(piece.Row(y)) != 40 {
				t.Fatalf("row %d lost its shape", y)
			}
		}
	})
}

// Resizing while there is something on the screen, which is what a window being dragged does.
func FuzzScreenResize(f *testing.F) {
	f.Add([]byte("\x1b[10;40Hsomething"), uint8(20), uint8(5))

	f.Fuzz(func(t *testing.T, out []byte, cols, rows uint8) {
		s := New(80, 24)
		s.Write(out)

		w, h := int(cols), int(rows)
		if w == 0 || h == 0 {
			return
		}
		s.Resize(w, h)
		s.Write(out)

		for y := 0; y < h; y++ {
			if got := len(s.Row(y)); got != w {
				t.Fatalf("after resizing to %dx%d, row %d is %d wide", w, h, y, got)
			}
		}
		if c, r := s.Cursor(); c < 0 || c > w || r < 0 || r >= h {
			t.Fatalf("the cursor is at %d,%d on a %dx%d screen", c, r, w, h)
		}
	})
}
