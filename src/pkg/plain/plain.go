// Package plain is text from somewhere else, made safe to put in front of a person.
//
// Anything a peer sends that drop then prints — what it calls itself, what it says a namespace is
// for, the name on a badge — is bytes chosen by somebody else and written to your terminal. A
// terminal is not a display; it is an interpreter. An escape in the middle of a listing moves the
// cursor up and rewrites the row above, so a peer can make its own entry claim to be read-only
// while the row it overwrote said something else entirely. The listing looks ordinary. That is the
// point of it.
//
// So nothing off the wire reaches a terminal without coming through here. What is left is one line
// of printable characters, of bounded length, in the order it will be read.
package plain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Most is how much of somebody else's text is worth showing when a caller has no opinion. Long
// enough for a sentence about a namespace, short enough that a peer cannot push the rest of a
// listing off the screen.
const Most = 120

// Text is a string from somewhere else, cut down to what can be printed.
//
// Removed rather than escaped: an escape shown as `\x1b[2K` is honest but it is also four times the
// width and nobody reading a listing wants it. What is dropped is exactly what could act on the
// terminal or lie about the order of what is left; ordinary writing in any language survives.
//
// The mark that says it was cut is inside the bound and not past it. Text and Fit have to agree
// about what is safe — one is what unsigned text is cleaned with and the other is what signed text
// is refused by, and a length they disagree on is a length where one of them is wrong.
func Text(s string, most int) string {
	if most <= 0 {
		most = Most
	}

	out := make([]rune, 0, most)
	cut := false
	for _, r := range s {
		if !shown(r) {
			continue
		}
		// Space before anything else is not worth the budget: it comes off the front at the end
		// anyway, and spending the room on it turns a long name into nothing but the mark. Every
		// kind of space, because a name padded with the unbreakable one is still a padded name.
		if unicode.IsSpace(r) && len(out) == 0 {
			continue
		}
		if len(out) == most {
			cut = true
			break
		}
		out = append(out, r)
	}

	if cut {
		out = append(out[:most-1], '…')
	}
	return strings.TrimSpace(string(out))
}

// Line is Text with the ordinary bound, for the callers that have no reason to pick one.
func Line(s string) string { return Text(s, Most) }

// shown reports whether a character can be put on a terminal as itself.
func shown(r rune) bool {
	switch {
	// What the decoder produces for bytes that were not UTF-8 at all. Somebody sending those is
	// not sending text.
	case r == utf8.RuneError:
		return false

	// Everything that acts on a terminal rather than appearing on it: the C0 controls with the
	// escape among them, delete, and the C1 range that some terminals still obey.
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return false

	// A space is a space; a tab moves to a column and breaks every layout it lands in.
	case r == ' ':
		return true

	// Marks that change the order text is read in, without changing the text. A name written with
	// one of these reads as something other than what it is, which is the whole trick.
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return false

	// Characters with no width: they hide inside a name and make two different names look like one.
	case r >= 0x200b && r <= 0x200f, r == 0xfeff, r == 0x00ad:
		return false

	// Anything the standard says is a format, surrogate, or unassigned control.
	case unicode.Is(unicode.Cf, r), unicode.Is(unicode.Cs, r), unicode.Is(unicode.Co, r):
		return false
	}
	return unicode.IsGraphic(r)
}

// Fit reports whether a string is already what Text would make of it, so a signed thing carrying a
// name can refuse one rather than quietly showing something else than what was signed.
//
// Refusing is right where sanitising is wrong. A badge is checked against bytes somebody signed: if
// the name were cleaned up on the way in, what is checked and what is shown would be two different
// strings, and the signature would cover only one of them.
func Fit(s string, most int) bool {
	if most <= 0 {
		most = Most
	}
	if utf8.RuneCountInString(s) > most {
		return false
	}
	for _, r := range s {
		if !shown(r) {
			return false
		}
	}
	return s == strings.TrimSpace(s)
}
