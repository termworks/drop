package tui

import "testing"

// A stream namespace is a command, not a terminal, so it has no size and reports zero. A screen
// that believes it has no cells left swallows everything written to it, and what a person sees is
// an empty pane that cannot be told from a stream sending nothing.
func TestAScreenIgnoresAnEmptySize(t *testing.T) {
	s := newScreen(80, 24)

	s.Resize(0, 0)
	if _, err := s.Write([]byte("tick 1")); err != nil {
		t.Fatalf("Write(): %v", err)
	}

	if drawn := s.Draw(); !contains(drawn, "tick 1") {
		t.Fatalf("what was written is not on the screen:\n%q", drawn)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// A screen is drawn into a view somebody else pads to the width of the window. A carriage return
// sends the cursor back to the start of the line just written, so the padding blanks it and the
// pane shows nothing at all — which is indistinguishable from a stream that sent nothing.
func TestADrawnScreenHasNoCarriageReturns(t *testing.T) {
	s := newScreen(20, 4)

	if _, err := s.Write([]byte("one\r\ntwo\r\n")); err != nil {
		t.Fatalf("Write(): %v", err)
	}

	drawn := s.Draw()
	if contains(drawn, "\r") {
		t.Fatalf("a drawn screen carries a carriage return:\n%q", drawn)
	}
	if !contains(drawn, "one") || !contains(drawn, "two") {
		t.Fatalf("what was written is not on the screen:\n%q", drawn)
	}
}
