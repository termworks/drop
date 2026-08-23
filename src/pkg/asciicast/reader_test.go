package asciicast

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func read(t *testing.T, cast string) (*Reader, Header) {
	t.Helper()

	r, head, err := NewReader(strings.NewReader(cast))
	if err != nil {
		t.Fatalf("NewReader(): %v", err)
	}
	return r, head
}

func TestReadsHeaderAndEvents(t *testing.T) {
	r, head := read(t, `{"version":2,"width":120,"height":40}
[0.1,"o","hello"]
[0.2,"r","100x30"]
[0.3,"m","password-on"]
`)

	if head.Width != 120 || head.Height != 40 {
		t.Fatalf("header = %dx%d, want 120x40", head.Width, head.Height)
	}

	want := []Event{
		{At: 0.1, Kind: Output, Data: "hello"},
		{At: 0.2, Kind: Resize, Data: "100x30"},
		{At: 0.3, Kind: Marker, Data: PasswordOn},
	}
	for i, w := range want {
		got, err := r.Next()
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if got != w {
			t.Fatalf("event %d = %+v, want %+v", i, got, w)
		}
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after the last event: %v, want EOF", err)
	}
}

// The marker is a security rule, not a nicety: whatever reads it must be able to recognise it, and
// a silent rename here would let output that should be wiped reach a watcher with nothing failing.
func TestPasswordMarkerIsRecognised(t *testing.T) {
	r, _ := read(t, `{"version":2,"width":80,"height":24}
[1.5,"m","password-on"]
[2.5,"m","password-off"]
`)

	on, err := r.Next()
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}
	if on.Kind != Marker || on.Data != PasswordOn {
		t.Fatalf("first marker = %+v, want kind %q data %q", on, Marker, PasswordOn)
	}

	off, err := r.Next()
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}
	if off.Data != PasswordOff {
		t.Fatalf("second marker = %q, want %q", off.Data, PasswordOff)
	}
}

// The stream is somebody else's output. One bad line is not a reason to stop showing a live
// terminal, so it is skipped and the next event still arrives.
func TestSkipsUnreadableLines(t *testing.T) {
	r, _ := read(t, `{"version":2,"width":80,"height":24}
not json at all
[]
[0.1,"o"]
["nan","o","x"]

[0.4,"o","survived"]
`)

	got, err := r.Next()
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}
	if got.Data != "survived" {
		t.Fatalf("got %+v, want the event after the bad lines", got)
	}
}

func TestRefusesWhatIsNotAsciicastV2(t *testing.T) {
	for name, cast := range map[string]string{
		"empty":         ``,
		"not json":      "this is not a header\n",
		"wrong version": `{"version":1,"width":80,"height":24}` + "\n",
	} {
		if _, _, err := NewReader(strings.NewReader(cast)); err == nil {
			t.Errorf("%s: NewReader() accepted it", name)
		}
	}
}

// A header without a usable size still has to yield one, or a watcher is told the terminal is 0x0.
func TestHeaderFallsBackToASensibleSize(t *testing.T) {
	_, head := read(t, `{"version":2}`+"\n")

	if head.Width <= 0 || head.Height <= 0 {
		t.Fatalf("header = %dx%d, want a usable default", head.Width, head.Height)
	}
}

func TestSize(t *testing.T) {
	cols, rows, ok := Size("120x40")
	if !ok || cols != 120 || rows != 40 {
		t.Fatalf("Size(120x40) = %d, %d, %v", cols, rows, ok)
	}
	for _, bad := range []string{"", "120", "x40", "wide x tall", "99999999x1"} {
		if _, _, ok := Size(bad); ok {
			t.Errorf("Size(%q) reported success", bad)
		}
	}
}

// Output carries whatever the terminal wrote, escapes included, and must come through byte for
// byte or a watcher renders something other than what was on the screen.
func TestOutputSurvivesEscapes(t *testing.T) {
	esc := "\\u001b"
	cast := `{"version":2,"width":80,"height":24}` + "\n" +
		`[0.1,"o","` + esc + `[2J` + esc + `[H$ ls\r\n"]` + "\n"

	r, _ := read(t, cast)

	got, err := r.Next()
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}

	want := "\x1b[2J\x1b[H$ ls\r\n"
	if got.Data != want {
		t.Fatalf("output = %q, want %q", got.Data, want)
	}
}
