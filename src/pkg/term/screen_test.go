package term

import (
	"strings"
	"testing"
)

// line reads a row back as text, with the padding a blank row is made of removed.
func line(s *Screen, y int) string {
	var b strings.Builder
	for _, c := range s.Row(y) {
		b.WriteRune(c.Ch)
	}
	return strings.TrimRight(b.String(), " ")
}

func write(t *testing.T, s *Screen, text string) {
	t.Helper()

	if _, err := s.Write([]byte(text)); err != nil {
		t.Fatalf("writing %q: %v", text, err)
	}
}

func TestPlainTextLandsOnTheFirstRow(t *testing.T) {
	s := New(20, 4)
	write(t, s, "hello")

	if got := line(s, 0); got != "hello" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestNewlineAndCarriageReturn(t *testing.T) {
	s := New(20, 4)
	write(t, s, "one\r\ntwo")

	if got := line(s, 0); got != "one" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := line(s, 1); got != "two" {
		t.Fatalf("row 1 = %q", got)
	}
}

func TestCarriageReturnAloneOverwrites(t *testing.T) {
	s := New(20, 4)
	write(t, s, "abcdef\rXY")

	if got := line(s, 0); got != "XYcdef" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestOutputPastTheLastRowScrolls(t *testing.T) {
	s := New(10, 3)
	write(t, s, "a\r\nb\r\nc\r\nd")

	if got := line(s, 0); got != "b" {
		t.Fatalf("row 0 = %q, want the second line after scrolling", got)
	}
	if got := line(s, 2); got != "d" {
		t.Fatalf("row 2 = %q", got)
	}
}

func TestWrappingAtTheRightEdge(t *testing.T) {
	s := New(4, 3)
	write(t, s, "abcdef")

	if got := line(s, 0); got != "abcd" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := line(s, 1); got != "ef" {
		t.Fatalf("row 1 = %q", got)
	}
}

func TestEraseDisplayClearsAndHomes(t *testing.T) {
	s := New(10, 3)
	write(t, s, "junk\r\nmore")
	write(t, s, "\x1b[2J\x1b[H")

	if got := line(s, 0); got != "" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := line(s, 1); got != "" {
		t.Fatalf("row 1 = %q", got)
	}
	if x, y := s.Cursor(); x != 0 || y != 0 {
		t.Fatalf("cursor = %d,%d", x, y)
	}
}

func TestCursorPositionAddressesRowAndColumn(t *testing.T) {
	s := New(10, 4)
	write(t, s, "\x1b[3;5Hx")

	if got := line(s, 2); got != "    x" {
		t.Fatalf("row 2 = %q", got)
	}
}

func TestEraseToEndOfLineLeavesTheStart(t *testing.T) {
	s := New(10, 2)
	write(t, s, "abcdefgh")
	write(t, s, "\x1b[1;4H\x1b[K")

	if got := line(s, 0); got != "abc" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestColourIsRecordedPerCell(t *testing.T) {
	s := New(10, 2)
	write(t, s, "\x1b[31mR\x1b[0mP")

	if got := s.Row(0)[0].FG; got != Indexed(1) {
		t.Fatalf("first cell = %+v, want indexed red", got)
	}
	if got := s.Row(0)[1].FG; got.Kind != ColorDefault {
		t.Fatalf("second cell = %+v, want the default", got)
	}
}

func TestBoldIsSetAndCleared(t *testing.T) {
	s := New(10, 2)
	write(t, s, "\x1b[1mB\x1b[22mN")

	if !s.Row(0)[0].Bold {
		t.Fatal("the first cell is not bold")
	}
	if s.Row(0)[1].Bold {
		t.Fatal("bold was not cleared")
	}
}

func TestTrueColourIsCarriedThrough(t *testing.T) {
	s := New(10, 2)
	write(t, s, "\x1b[38;2;10;20;30mX")

	if got := s.Row(0)[0].FG; got != RGB(10, 20, 30) {
		t.Fatalf("colour = %+v", got)
	}
}

func Test256ColourIsCarriedThrough(t *testing.T) {
	s := New(10, 2)
	write(t, s, "\x1b[38;5;200mX")

	if got := s.Row(0)[0].FG; got != Indexed(200) {
		t.Fatalf("colour = %+v", got)
	}
}

// A chunk boundary can fall anywhere, including the middle of an escape. Printing the tail as text
// is what a naive reader does, and it puts "[31m" on the screen.
func TestAnEscapeSplitAcrossWritesIsNotPrinted(t *testing.T) {
	s := New(10, 2)
	write(t, s, "A\x1b[3")
	write(t, s, "1mB")

	if got := line(s, 0); got != "AB" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := s.Row(0)[1].FG; got != Indexed(1) {
		t.Fatalf("the colour was lost across the split: %+v", got)
	}
}

// The same hazard for text: a multi-byte character can be cut in half by the network.
func TestACharacterSplitAcrossWritesIsRebuilt(t *testing.T) {
	both := []byte("é")
	if len(both) != 2 {
		t.Fatalf("expected a two byte character, got %d", len(both))
	}

	s := New(10, 2)
	if _, err := s.Write(both[:1]); err != nil {
		t.Fatalf("first half: %v", err)
	}
	if got := line(s, 0); got != "" {
		t.Fatalf("half a character was drawn: %q", got)
	}
	if _, err := s.Write(both[1:]); err != nil {
		t.Fatalf("second half: %v", err)
	}
	if got := line(s, 0); got != "é" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestAWindowTitleIsConsumedNotPrinted(t *testing.T) {
	s := New(20, 2)
	write(t, s, "\x1b]0;my title\x07done")

	if got := line(s, 0); got != "done" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestATitleSplitAcrossWritesIsStillConsumed(t *testing.T) {
	s := New(20, 2)
	write(t, s, "\x1b]0;my ti")
	write(t, s, "tle\x07done")

	if got := line(s, 0); got != "done" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestBackspaceMovesBackWithoutErasing(t *testing.T) {
	s := New(10, 2)
	write(t, s, "abc\bX")

	if got := line(s, 0); got != "abX" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestDeleteLinePullsTheOnesBelowUp(t *testing.T) {
	s := New(10, 4)
	write(t, s, "a\r\nb\r\nc")
	write(t, s, "\x1b[1;1H\x1b[M")

	if got := line(s, 0); got != "b" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := line(s, 1); got != "c" {
		t.Fatalf("row 1 = %q", got)
	}
}

func TestInsertLinePushesTheRestDown(t *testing.T) {
	s := New(10, 4)
	write(t, s, "a\r\nb")
	write(t, s, "\x1b[1;1H\x1b[L")

	if got := line(s, 0); got != "" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := line(s, 1); got != "a" {
		t.Fatalf("row 1 = %q", got)
	}
}

func TestDeleteCharsPullsTheRestOfTheLineLeft(t *testing.T) {
	s := New(10, 2)
	write(t, s, "abcdef")
	write(t, s, "\x1b[1;2H\x1b[2P")

	if got := line(s, 0); got != "adef" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestResizeStartsFromACleanGrid(t *testing.T) {
	s := New(10, 3)
	write(t, s, "stuff")
	s.Resize(20, 5)

	if cols, rows := s.Size(); cols != 20 || rows != 5 {
		t.Fatalf("size = %d,%d", cols, rows)
	}
	if got := line(s, 0); got != "" {
		t.Fatalf("row 0 = %q", got)
	}
}

// Nothing may write outside the grid, whatever the far end asks for. A terminal that could be
// talked past the end of a row would be a memory bug reachable from another machine.
func TestOutOfRangeMovesAreClamped(t *testing.T) {
	s := New(10, 3)

	write(t, s, "\x1b[99;99Hx")
	if x, y := s.Cursor(); x > 10 || y > 3 {
		t.Fatalf("cursor escaped the grid: %d,%d", x, y)
	}

	write(t, s, "\x1b[1;1H\x1b[999C\x1b[999B")
	if x, y := s.Cursor(); x >= 10 || y >= 3 {
		t.Fatalf("cursor escaped the grid: %d,%d", x, y)
	}

	write(t, s, "\x1b[999P\x1b[999X\x1b[999L\x1b[999M")
	if got := line(s, 0); got != "" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestAnUnknownSequenceIsIgnoredNotPrinted(t *testing.T) {
	s := New(20, 2)
	write(t, s, "a\x1b[?25lb")

	if got := line(s, 0); got != "ab" {
		t.Fatalf("row 0 = %q", got)
	}
}

func TestSaveAndRestoreTheCursor(t *testing.T) {
	s := New(20, 3)
	write(t, s, "\x1b[2;3H\x1b[s\x1b[1;1H\x1b[uX")

	if got := line(s, 1); got != "  X" {
		t.Fatalf("row 1 = %q", got)
	}
}
