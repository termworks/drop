package plain

import (
	"strings"
	"testing"
)

// The attack this exists for: a peer puts an escape in something drop prints, and the escape moves
// the cursor up and rewrites a row it did not send. What is left must not be able to do that.
func TestAnEscapeCannotReachTheTerminal(t *testing.T) {
	hostile := map[string]string{
		"rewrite the row above": "a chat\x1b[1A\x1b[2K  /secrets  files  read and write",
		"clear the screen":      "\x1b[2J\x1b[H owned",
		"set the window title":  "\x1b]0;not drop\x07",
		"a bare escape":         "\x1b",
		"carriage return":       "harmless\rmalicious",
		"a newline":             "one line\ntwo lines",
		"a tab":                 "col\tcol",
		"a nul":                 "before\x00after",
		"delete and C1":         "x\x7fy\x9bz",
	}

	for what, raw := range hostile {
		got := Text(raw, Most)
		for _, bad := range []string{"\x1b", "\r", "\n", "\t", "\x00", "\x7f", "\x9b", "\x07"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s: %q survived as %q", what, bad, got)
			}
		}
		if Fit(raw, Most) {
			t.Errorf("%s: %q was called fit to print", what, raw)
		}
	}
}

// Ordinary writing, in any language, has to come through untouched. A sanitiser that mangles names
// is one somebody turns off.
func TestOrdinaryWritingSurvives(t *testing.T) {
	for _, fine := range []string{
		"bresilla",
		"bob@laptop",
		"a directory, to walk through",
		"Trim Bresilla",
		"café",
		"日本語のノート",
		"Ελληνικά",
		"эксперимент",
		"a file, written by several people at once",
		"emoji are graphic: 🙂",
	} {
		if got := Text(fine, Most); got != fine {
			t.Errorf("%q came back as %q", fine, got)
		}
		if !Fit(fine, Most) {
			t.Errorf("%q was refused as unfit to print", fine)
		}
	}
}

// Text that reads as something other than what it is. The characters are invisible and reorder
// everything after them, so a name can be written to display as a different name entirely.
func TestTextThatLiesAboutItsOwnOrderIsRefused(t *testing.T) {
	for what, raw := range map[string]string{
		"right-to-left override": "annexe\u202egnp.eht",
		"left-to-right override": "safe\u202dunsafe",
		"an isolate":             "a\u2066b\u2069c",
		"zero width space":       "ad\u200bmin",
		"zero width joiner":      "ad\u200dmin",
		"a byte order mark":      "\ufeffadmin",
		"a soft hyphen":          "ad\u00admin",
	} {
		got := Text(raw, Most)
		for _, bad := range []rune{0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2069, 0x200b, 0x200d, 0xfeff, 0x00ad} {
			if strings.ContainsRune(got, bad) {
				t.Errorf("%s: %U survived in %q", what, bad, got)
			}
		}
		if Fit(raw, Most) {
			t.Errorf("%s: %q was called fit to print", what, raw)
		}
	}
}

// A peer must not be able to push the rest of a listing off the screen.
func TestSomebodyElsesTextIsBounded(t *testing.T) {
	long := strings.Repeat("a", 10_000)

	got := Text(long, 40)
	if n := len([]rune(got)); n > 41 {
		t.Fatalf("a 10,000 character name came back as %d characters", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a name that was cut does not say so: %q", got)
	}
	if Fit(long, 40) {
		t.Fatal("a 10,000 character name was called fit to print")
	}

	// And what fits is not cut.
	if got := Text("short", 40); got != "short" {
		t.Fatalf("a short name was changed to %q", got)
	}
}

// Bytes that were never text are not shown as the decoder's guess at them.
func TestBytesThatAreNotTextAreDropped(t *testing.T) {
	raw := "ok\xff\xfe\xfdthen"

	got := Text(raw, Most)
	if got != "okthen" {
		t.Fatalf("invalid utf-8 came back as %q", got)
	}
	if Fit(raw, Most) {
		t.Fatal("invalid utf-8 was called fit to print")
	}
}

// Nothing in, nothing out — and a name that is only spaces is not a name.
func TestEmptyAndBlankComeBackEmpty(t *testing.T) {
	for _, blank := range []string{"", " ", "   ", "\x1b\x1b", "\u200b\u200b"} {
		if got := Text(blank, Most); got != "" {
			t.Errorf("%q came back as %q", blank, got)
		}
	}
}
