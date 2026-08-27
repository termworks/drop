package plain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The last thing between somebody else's bytes and a terminal.
//
// Everything else can be careful and it still comes down to this: whatever goes in, what comes out
// must be printable, bounded, and valid text. If any input at all gets an escape through, every
// listing and every conversation is a place to write on somebody's screen.
func FuzzText(f *testing.F) {
	f.Add("a chat\x1b[1A\x1b[2K  /secrets", 120)
	f.Add("\x1b]0;title\x07", 10)
	f.Add("annexe‮gnp.eht", 120)
	f.Add("\xff\xfe\xfd", 120)
	f.Add(strings.Repeat("a", 5000), 3)
	f.Add("", 0)
	f.Add(" ", -1)

	f.Fuzz(func(t *testing.T, raw string, most int) {
		got := Text(raw, most)

		if !utf8.ValidString(got) {
			t.Fatalf("%q came out as bytes that are not text", raw)
		}
		for _, r := range got {
			// The ellipsis is this package's own doing and is printable; everything else has to
			// have survived the same test the package applies.
			if r == '…' {
				continue
			}
			if !shown(r) {
				t.Fatalf("%q let %U through", raw, r)
			}
		}
		if strings.TrimSpace(got) != got {
			t.Fatalf("%q came out with space on the end: %q", raw, got)
		}

		// Bounded, whatever was asked for.
		limit := most
		if limit <= 0 {
			limit = Most
		}
		if n := utf8.RuneCountInString(got); n > limit+1 {
			t.Fatalf("a bound of %d let %d characters through", limit, n)
		}

		// And what comes out is always fit, or Text and Fit disagree about what is safe — which is
		// how a signed thing gets refused for something the unsigned path would have shown.
		if got != "" && !Fit(got, limit) {
			t.Fatalf("Text made %q out of %q, and Fit calls it unfit", got, raw)
		}

		// Idempotent: cleaning what is already clean must change nothing.
		if again := Text(got, limit); again != got {
			t.Fatalf("cleaning %q twice gave %q then %q", raw, got, again)
		}
	})
}

// Fit is what a signed thing is held to, so it must never call something safe that Text would have
// had to change.
func FuzzFitAgreesWithText(f *testing.F) {
	f.Add("bresilla", 64)
	f.Add("root\x1b[1A", 64)
	f.Add("日本語", 64)

	f.Fuzz(func(t *testing.T, raw string, most int) {
		if !Fit(raw, most) {
			return
		}
		if got := Text(raw, most); got != raw {
			t.Fatalf("Fit called %q safe and Text makes it %q", raw, got)
		}
	})
}
