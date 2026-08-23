package term

import (
	"strings"
	"testing"
)

// The round trip is the real test: whatever we emit, reading it back must rebuild the same grid.
// Anything else means a viewer sees something the sender never drew.
func TestWhatIsDrawnReadsBackTheSame(t *testing.T) {
	first := New(40, 4)
	write(t, first, "\x1b[1;31mred bold\x1b[0m plain \x1b[4munder\x1b[0m\r\n\x1b[38;5;200mfancy\x1b[0m")

	second := New(40, 4)
	write(t, second, first.ANSI())

	for y := range 4 {
		for x := range 40 {
			a, b := first.Row(y)[x], second.Row(y)[x]
			if a != b {
				t.Fatalf("row %d col %d: %+v became %+v", y, x, a, b)
			}
		}
	}
}

func TestTrueColourSurvivesTheRoundTrip(t *testing.T) {
	first := New(20, 2)
	write(t, first, "\x1b[38;2;10;20;30mX\x1b[48;2;40;50;60mY")

	second := New(20, 2)
	write(t, second, first.ANSI())

	if got := second.Row(0)[0].FG; got != RGB(10, 20, 30) {
		t.Fatalf("foreground = %+v", got)
	}
	if got := second.Row(0)[1].BG; got != RGB(40, 50, 60) {
		t.Fatalf("background = %+v", got)
	}
}

// Every run starts from a reset, so a frame joined halfway cannot inherit a colour from a run the
// viewer never saw.
func TestEveryRunStartsFromAKnownState(t *testing.T) {
	s := New(20, 1)
	write(t, s, "\x1b[31mred\x1b[32mgreen")

	drawn := s.ANSI()
	for _, run := range strings.Split(drawn, "\x1b[")[1:] {
		if run == "0m" || strings.HasPrefix(run, "0;") {
			continue
		}
		if strings.HasSuffix(run, "m") && !strings.HasPrefix(run, "0") {
			t.Fatalf("a run did not reset first: %q in %q", run, drawn)
		}
	}
}

func TestTrailingBlanksAreNotDrawn(t *testing.T) {
	s := New(200, 2)
	write(t, s, "hi")

	if got := len(s.ANSI()); got > 40 {
		t.Fatalf("a two character line drew %d bytes", got)
	}
}

func TestTheRowCountIsKept(t *testing.T) {
	s := New(20, 5)
	write(t, s, "one\r\ntwo")

	if got := strings.Count(s.ANSI(), "\n"); got != 4 {
		t.Fatalf("a five row screen drew %d newlines, want 4", got)
	}
}
