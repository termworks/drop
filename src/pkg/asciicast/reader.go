// Package asciicast reads the asciicast v2 stream a terminal recorder produces.
//
// JSON, unlike the rest of drop's wire formats, because this one is not drop's: it is what
// asciinema writes and what hexe hands a stream backend, so it is read as specified.
package asciicast

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// What an event can be.
const (
	// Output is what the program wrote. This is the one a viewer draws.
	Output = "o"
	// Input is what was typed, when the recorder captures it.
	Input = "i"
	// Marker is an out-of-band note about the session, such as a password prompt.
	Marker = "m"
	// Resize reports the terminal's new size, as "120x40".
	Resize = "r"
)

// PasswordOn is the marker that means everything after it is being typed into a prompt that must
// not be recorded. A reader keeping scrollback has to clear it, not merely stop appending: the
// bytes that drew the prompt went out before anything could know, so they are already kept.
const PasswordOn = "password-on"

// PasswordOff means the prompt is gone and output may be shown again.
const PasswordOff = "password-off"

// Header is the first line of a cast.
type Header struct {
	Version int `json:"version"`
	Width   int `json:"width"`
	Height  int `json:"height"`
}

// Event is one line after the header: a time, a kind, and its data.
type Event struct {
	At   float64
	Kind string
	Data string
}

// Reader takes a cast apart, one event at a time.
type Reader struct {
	lines *bufio.Scanner
}

// MaxLine bounds a single event. A terminal writes a lot at once, and a line longer than this is
// not a cast being read but something else on the pipe.
const MaxLine = 4 << 20

// NewReader reads the header, so what follows is events.
func NewReader(r io.Reader) (*Reader, Header, error) {
	var head Header

	lines := bufio.NewScanner(r)
	lines.Buffer(make([]byte, 0, 64<<10), MaxLine)

	if !lines.Scan() {
		if err := lines.Err(); err != nil {
			return nil, head, fmt.Errorf("reading the cast header: %w", err)
		}
		return nil, head, fmt.Errorf("the cast is empty")
	}
	if err := json.Unmarshal(lines.Bytes(), &head); err != nil {
		return nil, head, fmt.Errorf("reading the cast header: %w", err)
	}
	if head.Version != 2 {
		return nil, head, fmt.Errorf("this is asciicast v%d; only v2 is understood", head.Version)
	}
	if head.Width <= 0 || head.Height <= 0 {
		head.Width, head.Height = 80, 24
	}

	return &Reader{lines: lines}, head, nil
}

// Next reads one event. It returns io.EOF when the cast ends.
//
// A line that does not parse is skipped rather than fatal: the stream is somebody else's output,
// and one malformed line is not a reason to stop showing the rest of a live terminal.
func (r *Reader) Next() (Event, error) {
	for {
		if !r.lines.Scan() {
			if err := r.lines.Err(); err != nil {
				return Event{}, err
			}
			return Event{}, io.EOF
		}

		line := strings.TrimSpace(r.lines.Text())
		if line == "" {
			continue
		}

		var raw []json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil || len(raw) < 3 {
			continue
		}

		at, err := strconv.ParseFloat(string(raw[0]), 64)
		if err != nil {
			continue
		}
		var kind, data string
		if json.Unmarshal(raw[1], &kind) != nil || json.Unmarshal(raw[2], &data) != nil {
			continue
		}

		return Event{At: at, Kind: kind, Data: data}, nil
	}
}

// Size reads a resize event's data, "120x40".
func Size(data string) (cols, rows uint16, ok bool) {
	w, h, found := strings.Cut(data, "x")
	if !found {
		return 0, 0, false
	}

	width, err := strconv.ParseUint(w, 10, 16)
	if err != nil {
		return 0, 0, false
	}
	height, err := strconv.ParseUint(h, 10, 16)
	if err != nil {
		return 0, 0, false
	}
	return uint16(width), uint16(height), true
}
