package term

import (
	"strconv"
	"strings"
)

// ANSI draws the screen as text a terminal can print.
//
// The grid is the source of truth — it has already resolved every scroll, erase and cursor move — so
// what comes out is one clean frame rather than the stream of edits that produced it. That is what
// makes a late viewer see the same picture as an early one.
func (s *Screen) ANSI() string {
	var out strings.Builder

	for y := range s.rows {
		row := s.grid[y]

		end := len(row)
		for end > 0 && row[end-1] == blank() {
			end--
		}

		start := 0
		for i := 1; i <= end; i++ {
			if i == end || row[i].Style != row[start].Style {
				out.WriteString(sgr(row[start].Style))
				for _, c := range row[start:i] {
					out.WriteRune(c.Ch)
				}
				start = i
			}
		}
		if end > 0 {
			out.WriteString("\x1b[0m")
		}
		if y < s.rows-1 {
			// Carriage return as well as line feed: a line feed alone moves down without returning to
			// column one, so every row after the first would start where the last one ended.
			out.WriteString("\r\n")
		}
	}
	return out.String()
}

// sgr is the escape that puts a terminal into one style, from a known-clean state.
//
// Always reset first: a run says what it is rather than what changed since the last one, so a frame
// dropped in halfway cannot inherit a colour from a run nobody saw.
func sgr(style Style) string {
	codes := []string{"0"}

	if style.Bold {
		codes = append(codes, "1")
	}
	if style.Dim {
		codes = append(codes, "2")
	}
	if style.Italic {
		codes = append(codes, "3")
	}
	if style.Underline {
		codes = append(codes, "4")
	}
	if style.Inverse {
		codes = append(codes, "7")
	}

	codes = append(codes, colour(style.FG, false)...)
	codes = append(codes, colour(style.BG, true)...)

	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// colour renders one half of a style. The named sixteen have their own codes; everything above them
// goes through the extended form.
func colour(c Color, background bool) []string {
	base, extended := 30, "38"
	if background {
		base, extended = 40, "48"
	}

	switch c.Kind {
	case ColorIndexed:
		switch {
		case c.N < 8:
			return []string{strconv.Itoa(base + int(c.N))}
		case c.N < 16:
			return []string{strconv.Itoa(base + 60 + int(c.N-8))}
		default:
			return []string{extended, "5", strconv.Itoa(int(c.N))}
		}
	case ColorRGB:
		return []string{extended, "2", strconv.Itoa(int(c.R)), strconv.Itoa(int(c.G)), strconv.Itoa(int(c.B))}
	}
	return nil
}
