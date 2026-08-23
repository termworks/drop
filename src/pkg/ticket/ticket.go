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
// Medium correction, which tolerates about 15% damage: this is read off a screen by a camera a foot
// away, not off a label on a crate, and every level above medium costs modules and therefore columns
// in a terminal that has only so many.
func Code(text string) (*qr.Code, error) {
	code, err := qr.Encode(Link(text), qr.M)
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

// black reports whether a module is set, treating everything outside the code as light so the quiet
// zone comes out blank rather than out of range.
func black(code *qr.Code, x, y int) bool {
	if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
		return false
	}
	return code.Black(x, y)
}
