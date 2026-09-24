// Package ticket carries a pairing invitation between two devices.
//
// The same invitation three ways: as text to paste, as a link to tap, and as a QR to point a camera
// at. A phone has no shared clipboard with a workstation, which is the case the other two do not
// cover.
package ticket

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// Scheme is what a link is registered under, so tapping one reaches drop rather than a search engine.
const Scheme = "drop"

// Link renders a ticket as something tappable.
func Link(text string) string {
	return Scheme + "://pair/" + text
}

// FromLink takes a ticket back out of a link, and passes plain text through unchanged so a caller
// can accept either without asking which it was given.
func FromLink(text string) string {
	text = strings.TrimSpace(text)

	for _, prefix := range []string{Scheme + "://pair/", Scheme + ":pair/", Scheme + "://"} {
		if rest, found := strings.CutPrefix(text, prefix); found {
			return strings.Trim(rest, "/")
		}
	}
	return text
}

// Code renders a ticket as a QR code.
//
// The lowest correction level, deliberately. Correction is there for a code that gets scuffed,
// and this one is read off a screen a foot from a camera — nothing is going to damage it. The
// levels above it cost modules, and modules are terminal rows: at M a ticket comes out four
// rows taller, which is the difference between drawing it and telling somebody their window is
// too small.
func Code(text string) (*qr.Code, error) {
	code, err := qr.Encode(Link(text), qr.L)
	if err != nil {
		return nil, fmt.Errorf("encoding the ticket: %w", err)
	}
	return code, nil
}

// Render draws a QR code for a terminal.
//
// Two rows of modules per line of text, as half blocks: a terminal cell is about twice as tall as it
// is wide, so one cell per module comes out stretched and a phone camera struggles with it. The quiet
// zone is not decoration — a reader needs the margin to find the edges.
func Render(code *qr.Code) string {
	const quiet = 2

	size := code.Size
	var out strings.Builder

	for y := -quiet; y < size+quiet; y += 2 {
		for x := -quiet; x < size+quiet; x++ {
			top, bottom := black(code, x, y), black(code, x, y+1)

			switch {
			case top && bottom:
				out.WriteString("█")
			case top:
				out.WriteString("▀")
			case bottom:
				out.WriteString("▄")
			default:
				out.WriteString(" ")
			}
		}
		out.WriteString("\n")
	}
	return out.String()
}

// Painted is Render with its colours fixed: black modules on a white ground.
//
// Left to the terminal, a module is drawn in whatever the foreground is, and on a dark theme that
// is a light code on a dark ground — inverted, which plenty of cameras will not read. Colours from a
// palette are no better, because a theme repaints those too and black comes out grey. So each cell
// says both of its colours exactly, and a cell that is all one colour is painted as ground rather
// than drawn as a glyph: a block character is the font's to draw, with its seams, and a camera
// reads those as grey.
func Painted(code *qr.Code) string {
	const (
		quiet    = 2
		allInk   = "\x1b[48;2;0;0;0m "
		allPaper = "\x1b[48;2;255;255;255m "
		inkAbove = "\x1b[38;2;0;0;0;48;2;255;255;255m\u2580"
		inkBelow = "\x1b[38;2;255;255;255;48;2;0;0;0m\u2580"
		reset    = "\x1b[0m"
	)

	size := code.Size
	var out strings.Builder
	for y := -quiet; y < size+quiet; y += 2 {
		for x := -quiet; x < size+quiet; x++ {
			top, bottom := black(code, x, y), black(code, x, y+1)
			switch {
			case top && bottom:
				out.WriteString(allInk)
			case top:
				out.WriteString(inkAbove)
			case bottom:
				out.WriteString(inkBelow)
			default:
				out.WriteString(allPaper)
			}
		}
		out.WriteString(reset + "\n")
	}
	return out.String()
}

// black reports whether a module is set, treating everything outside the code as light so the quiet
// zone comes out blank rather than out of range.
func black(code *qr.Code, x, y int) bool {
	if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
		return false
	}
	return code.Black(x, y)
}

// Wide draws a QR code where a module is two characters across and one line down.
//
// Render halves the rows because a terminal cell is about twice as tall as it is wide. A proportional
// layout engine drawing a monospace face has no such ratio — a character is nearer 0.6 of the line
// height — so halving there squashes the code, and a squashed code is one a camera gives up on. Two
// characters per module and no halving comes out square enough to read.
func Wide(code *qr.Code) string {
	const quiet = 2

	size := code.Size
	var out strings.Builder

	for y := -quiet; y < size+quiet; y++ {
		for x := -quiet; x < size+quiet; x++ {
			if black(code, x, y) {
				out.WriteString("██")
				continue
			}
			out.WriteString("  ")
		}
		out.WriteString("\n")
	}
	return out.String()
}

// A pairing ticket is one thing a camera carries between two machines. A badge one machine signs
// for another is a second, and a user key carried over is a third; each is a link of its own kind,
// so whatever reads a code knows which it was handed.
const (
	KindBadge = "badge"
	KindKey   = "key"
)

// LinkAs is a code of one kind as a link.
func LinkAs(kind, text string) string { return Scheme + "://" + kind + "/" + text }

// Kind says what a link is and hands back what it carries. A pairing ticket, linked or bare, is
// "pair".
func Kind(text string) (string, string) {
	text = strings.TrimSpace(text)
	for _, kind := range []string{KindBadge, KindKey} {
		if rest, found := strings.CutPrefix(text, LinkAs(kind, "")); found {
			return kind, strings.Trim(rest, "/")
		}
	}
	return "pair", FromLink(text)
}

// CodeOf draws a whole link as a QR code, the way Code draws a ticket.
func CodeOf(link string) (*qr.Code, error) {
	code, err := qr.Encode(link, qr.L)
	if err != nil {
		return nil, fmt.Errorf("encoding the code: %w", err)
	}
	return code, nil
}
