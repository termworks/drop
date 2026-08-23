package ticket

import (
	"strings"
	"testing"
)

const sample = "7b9773d9686b7fd24dcbe88c5a101401ab1f7fbbbe27b9685592937c2f93f560#fqdv-q64c-ebfl#192.168.1.157:47901"

func TestALinkRoundTrips(t *testing.T) {
	if got := FromLink(Link(sample)); got != sample {
		t.Fatalf("round trip = %q", got)
	}
}

// Whoever is pasting has no reason to know which form they were handed, so both must work.
func TestPlainTextPassesThrough(t *testing.T) {
	if got := FromLink(sample); got != sample {
		t.Fatalf("plain text was mangled: %q", got)
	}
	if got := FromLink("  " + sample + "\n"); got != sample {
		t.Fatalf("surrounding space was not trimmed: %q", got)
	}
}

func TestTheOtherLinkShapes(t *testing.T) {
	for _, form := range []string{
		"drop://pair/" + sample,
		"drop:pair/" + sample,
		"drop://" + sample,
	} {
		if got := FromLink(form); got != sample {
			t.Fatalf("%q gave %q", form, got)
		}
	}
}

func TestACodeIsProducedAndFitsATerminal(t *testing.T) {
	code, err := Code(sample)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	drawn := Render(code)
	lines := strings.Split(strings.TrimRight(drawn, "\n"), "\n")

	if len(lines) == 0 {
		t.Fatal("nothing was drawn")
	}

	// Two module rows per line of text, plus the quiet zone on both sides.
	width := len([]rune(lines[0]))
	if want := code.Size + 4; width != want {
		t.Fatalf("width = %d modules, want %d", width, want)
	}
	// A terminal is usually 80 columns, and a ticket that will not fit is one nobody can scan.
	if width > 80 {
		t.Fatalf("a %d column code will not fit an ordinary terminal", width)
	}

	for i, line := range lines {
		if got := len([]rune(line)); got != width {
			t.Fatalf("line %d is %d wide, not %d — the code is not rectangular", i, got, width)
		}
	}
}

// The margin is what a reader uses to find the edges. Without it a camera sees no code at all.
func TestTheQuietZoneIsBlank(t *testing.T) {
	code, err := Code(sample)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	lines := strings.Split(strings.TrimRight(Render(code), "\n"), "\n")

	if strings.TrimSpace(lines[0]) != "" {
		t.Fatal("the top margin has something in it")
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "" {
		t.Fatal("the bottom margin has something in it")
	}
	for i, line := range lines {
		runes := []rune(line)
		if runes[0] != ' ' || runes[1] != ' ' {
			t.Fatalf("line %d has no left margin", i)
		}
	}
}

func TestAnEmptyTicketIsStillEncodable(t *testing.T) {
	if _, err := Code(""); err != nil {
		t.Fatalf("the scheme alone should encode: %v", err)
	}
}
