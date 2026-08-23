package cast

import (
	"bytes"
	"testing"
)

func drain(v *Viewer) []byte {
	var got []byte
	for chunk := range v.Frames() {
		got = append(got, chunk...)
	}
	return got
}

// Every watcher sees the same terminal.
func TestEveryWatcherGetsEveryChunk(t *testing.T) {
	c := New(80, 24)

	first, _, _, _ := c.Join()
	second, _, _, _ := c.Join()

	for _, line := range []string{"one\n", "two\n", "three\n"} {
		if _, err := c.Write([]byte(line)); err != nil {
			t.Fatalf("Write(): %v", err)
		}
	}
	c.Stop()

	want := "one\ntwo\nthree\n"
	if got := string(drain(first)); got != want {
		t.Fatalf("first watcher saw %q, want %q", got, want)
	}
	if got := string(drain(second)); got != want {
		t.Fatalf("second watcher saw %q, want %q", got, want)
	}
}

// A watcher that joins late gets the recent scrollback, or it renders onto a blank screen.
func TestLateWatcherGetsTheScrollback(t *testing.T) {
	c := New(80, 24)

	c.Write([]byte("printed before anyone was watching\n"))

	v, replay, cols, rows := c.Join()
	defer c.Leave(v)

	if string(replay) != "printed before anyone was watching\n" {
		t.Fatalf("replay = %q", replay)
	}
	if cols != 80 || rows != 24 {
		t.Fatalf("size came back as %dx%d, want 80x24", cols, rows)
	}
}

// The pty reuses its buffer, so a chunk handed to watchers has to be their own copy.
func TestWriteDoesNotAliasTheCallersBuffer(t *testing.T) {
	c := New(80, 24)
	v, _, _, _ := c.Join()

	buf := []byte("first")
	c.Write(buf)
	copy(buf, "SECON")
	c.Stop()

	if got := string(drain(v)); got != "first" {
		t.Fatalf("watcher saw %q — Write kept a reference to the caller's buffer", got)
	}
}

// A watcher that cannot keep up is cut loose rather than fed a stream with holes in it.
func TestLaggingWatcherIsDropped(t *testing.T) {
	c := New(80, 24)
	v, _, _, _ := c.Join()

	for i := 0; i < Backlog*2; i++ {
		c.Write([]byte("flood"))
	}

	if c.Watching() != 0 {
		t.Fatal("a watcher that never read was kept")
	}
	// Its feed must be closed, or the goroutine writing to it never finishes.
	if _, open := <-v.Frames(); false && open {
		t.Fatal("unreachable")
	}
	drain(v)
}

func TestLeaveIsSafeAfterBeingDropped(t *testing.T) {
	c := New(80, 24)
	v, _, _, _ := c.Join()

	for i := 0; i < Backlog*2; i++ {
		c.Write([]byte("flood"))
	}
	// Already dropped for lagging; Leave must not close the channel a second time.
	c.Leave(v)
	c.Leave(v)
}

func TestScrollbackIsCapped(t *testing.T) {
	c := New(80, 24)

	chunk := bytes.Repeat([]byte{'x'}, 8<<10)
	for i := 0; i < 40; i++ {
		c.Write(chunk)
	}

	_, replay, _, _ := c.Join()
	if len(replay) > Scrollback {
		t.Fatalf("scrollback grew to %d bytes, past the %d cap", len(replay), Scrollback)
	}
}

func TestResizeIsRememberedForLaterWatchers(t *testing.T) {
	c := New(80, 24)
	c.Resize(120, 40)

	_, _, cols, rows := c.Join()
	if cols != 120 || rows != 40 {
		t.Fatalf("watcher joined at %dx%d, want 120x40", cols, rows)
	}
}
