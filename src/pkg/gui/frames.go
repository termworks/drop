//go:build js || android

package gui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bresilla/drop/src/pkg/term"
)

// readFrames turns the bridge's event stream back into terminal bytes.
//
// The bridge sends a screen that has already been parsed — rows of styled runs — rather than the
// stream that produced it. Drawing those back as ANSI lets the same term.Screen sit behind every
// build of this interface, whether it read the wire itself or was handed a picture of it.
func readFrames(from io.Reader, into io.Writer, resize func(cols, rows int)) error {
	lines := bufio.NewScanner(from)
	lines.Buffer(make([]byte, 0, 64*1024), 4<<20)

	for lines.Scan() {
		data, found := strings.CutPrefix(lines.Text(), "data: ")
		if !found {
			continue
		}

		var frame term.Frame
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			continue
		}

		if frame.Cols > 0 && frame.Rows > 0 && resize != nil {
			resize(frame.Cols, frame.Rows)
		}
		if _, err := io.WriteString(into, redraw(frame)); err != nil {
			return err
		}
	}

	if err := lines.Err(); err != nil && !strings.Contains(err.Error(), "closed") {
		return err
	}
	return nil
}

// redraw turns one frame into the escapes that put it on a screen.
//
// Rows are addressed rather than printed in order, because a frame carries only what changed: a
// terminal scrolling one line should move one line, not repaint eighty.
func redraw(frame term.Frame) string {
	var out strings.Builder

	for at, runs := range frame.Lines {
		// Rows are one-based in a terminal and zero-based in a frame.
		fmt.Fprintf(&out, "\x1b[%d;1H\x1b[2K", at+1)

		for _, run := range runs {
			out.WriteString(escapes(run))
			out.WriteString(run.Text)
		}
		out.WriteString("\x1b[0m")
	}

	if len(frame.Cursor) == 2 {
		fmt.Fprintf(&out, "\x1b[%d;%dH", frame.Cursor[1]+1, frame.Cursor[0]+1)
	}
	return out.String()
}

// escapes renders one run's styling. The colours arrive as the strings the page would use, so they
// are turned back into the codes a terminal understands.
func escapes(run term.Run) string {
	codes := []string{"0"}

	if run.Bold {
		codes = append(codes, "1")
	}
	if run.Dim {
		codes = append(codes, "2")
	}
	if run.Ital {
		codes = append(codes, "3")
	}
	if run.Und {
		codes = append(codes, "4")
	}

	if c := fromCSS(run.FG, false); c != "" {
		codes = append(codes, c)
	}
	if c := fromCSS(run.BG, true); c != "" {
		codes = append(codes, c)
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// fromCSS reads back what term.css wrote: either one of the sixteen named slots, or an exact colour.
func fromCSS(text string, background bool) string {
	if text == "" {
		return ""
	}

	base, extended := 30, "38"
	if background {
		base, extended = 40, "48"
	}

	var n int
	if _, err := fmt.Sscanf(text, "var(--t%d)", &n); err == nil {
		if n < 8 {
			return fmt.Sprint(base + n)
		}
		return fmt.Sprint(base + 60 + n - 8)
	}

	var r, g, b int
	if _, err := fmt.Sscanf(text, "rgb(%d,%d,%d)", &r, &g, &b); err == nil {
		return fmt.Sprintf("%s;2;%d;%d;%d", extended, r, g, b)
	}
	return ""
}
