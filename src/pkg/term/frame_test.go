package term

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTheFirstFrameDescribesTheWholeScreen(t *testing.T) {
	s := New(20, 3)
	write(t, s, "hello")

	f := NewPainter().Frame(s)
	if f.Cols != 20 || f.Rows != 3 {
		t.Fatalf("size = %d,%d", f.Cols, f.Rows)
	}
	if len(f.Lines[0]) != 1 || f.Lines[0][0].Text != "hello" {
		t.Fatalf("row 0 = %+v", f.Lines[0])
	}
}

// The point of a frame is that it is small. A screen where one line changed must not resend the
// other twenty-three.
func TestOnlyChangedRowsAreSent(t *testing.T) {
	s := New(20, 5)
	write(t, s, "a\r\nb\r\nc")

	p := NewPainter()
	p.Frame(s)

	write(t, s, "\x1b[2;1HX")

	f := p.Frame(s)
	if len(f.Lines) != 1 {
		t.Fatalf("sent %d rows, want 1: %+v", len(f.Lines), f.Lines)
	}
	if _, ok := f.Lines[1]; !ok {
		t.Fatalf("the changed row was not the one sent: %+v", f.Lines)
	}
}

func TestAnUnchangedScreenSendsNothing(t *testing.T) {
	s := New(20, 3)
	write(t, s, "steady")

	p := NewPainter()
	p.Frame(s)

	if f := p.Frame(s); !f.Empty() {
		t.Fatalf("a still screen produced %+v", f)
	}
}

func TestAResizeResendsEverything(t *testing.T) {
	s := New(20, 3)
	write(t, s, "before")

	p := NewPainter()
	p.Frame(s)

	s.Resize(30, 4)
	write(t, s, "after")

	f := p.Frame(s)
	if f.Cols != 30 || f.Rows != 4 {
		t.Fatalf("size = %d,%d", f.Cols, f.Rows)
	}
	if len(f.Lines) == 0 {
		t.Fatal("a resize sent no content")
	}
}

func TestRunsCollapseByStyle(t *testing.T) {
	s := New(20, 2)
	write(t, s, "\x1b[31mred\x1b[0mplain")

	f := NewPainter().Frame(s)
	got := f.Lines[0]
	if len(got) != 2 {
		t.Fatalf("want 2 runs, got %d: %+v", len(got), got)
	}
	if got[0].Text != "red" || got[0].FG != "var(--t1)" {
		t.Fatalf("first run = %+v", got[0])
	}
	if got[1].Text != "plain" || got[1].FG != "" {
		t.Fatalf("second run = %+v", got[1])
	}
}

func TestTrailingBlanksAreNotSent(t *testing.T) {
	s := New(200, 2)
	write(t, s, "hi")

	f := NewPainter().Frame(s)
	if total := length(f.Lines[0]); total != 2 {
		t.Fatalf("sent %d characters for a two character line", total)
	}
}

func TestInverseNamesBothSides(t *testing.T) {
	s := New(20, 2)
	write(t, s, "\x1b[7mX")

	f := NewPainter().Frame(s)
	got := f.Lines[0][0]
	if got.FG != "var(--term-bg)" || got.BG != "var(--term-fg)" {
		t.Fatalf("inverse produced %+v", got)
	}
}

func Test256ColoursResolveToRGB(t *testing.T) {
	s := New(20, 2)
	write(t, s, "\x1b[38;5;196mX")

	f := NewPainter().Frame(s)
	if got := f.Lines[0][0].FG; got != "rgb(255,0,0)" {
		t.Fatalf("colour 196 = %q", got)
	}
}

func TestGreyRampResolves(t *testing.T) {
	s := New(20, 2)
	write(t, s, "\x1b[38;5;232mX")

	f := NewPainter().Frame(s)
	if got := f.Lines[0][0].FG; got != "rgb(8,8,8)" {
		t.Fatalf("colour 232 = %q", got)
	}
}

// Whatever the far end draws, a frame is data. Nothing in it may be read by the page as markup.
func TestOutputStaysDataInTheFrame(t *testing.T) {
	s := New(60, 2)
	write(t, s, `<script>alert(1)</script>`)

	f := NewPainter().Frame(s)
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	// It survives as text, and JSON encoding is what carries it — there is no HTML here to escape.
	var back Frame
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got := text(back.Lines[0]); !strings.HasPrefix(got, "<script>alert(1)</script>") {
		t.Fatalf("round trip = %q", got)
	}
}

func TestTheCursorIsSentOnlyWhenItMoves(t *testing.T) {
	s := New(20, 3)
	write(t, s, "ab")

	p := NewPainter()
	p.Frame(s)

	write(t, s, "\x1b[3;1H")
	if f := p.Frame(s); f.Cursor == nil {
		t.Fatal("a moved cursor was not reported")
	}
	if f := p.Frame(s); f.Cursor != nil {
		t.Fatalf("a still cursor was reported: %v", f.Cursor)
	}
}

func length(runs []Run) int {
	n := 0
	for _, r := range runs {
		n += len([]rune(r.Text))
	}
	return n
}

func text(runs []Run) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

// An idle 80x24 screen is almost entirely blank. Sending a null for every empty row costs more
// than the content does.
func TestBlankRowsAreNotSentOnTheFirstFrame(t *testing.T) {
	s := New(80, 24)
	write(t, s, "$ ")

	f := NewPainter().Frame(s)
	if len(f.Lines) != 1 {
		t.Fatalf("sent %d rows for a one line screen: %+v", len(f.Lines), f.Lines)
	}

	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if strings.Contains(string(raw), "null") {
		t.Fatalf("empty rows travelled as nulls: %s", raw)
	}
}

// Clearing a line is a change like any other, and the watcher has to be told or the old text stays
// on its screen forever.
func TestARowThatBecomesEmptyIsStillSent(t *testing.T) {
	s := New(20, 3)
	write(t, s, "something")

	p := NewPainter()
	p.Frame(s)

	write(t, s, "\x1b[1;1H\x1b[2K")

	f := p.Frame(s)
	runs, ok := f.Lines[0]
	if !ok {
		t.Fatalf("the cleared row was not sent: %+v", f.Lines)
	}
	if len(runs) != 0 {
		t.Fatalf("the cleared row still carries %+v", runs)
	}
}
