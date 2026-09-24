package cast

import (
	"bytes"
	"testing"
	"time"
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

// A watcher that joins late is handed the screen as it stands, or it renders onto a blank one.
func TestLateWatcherGetsTheScreen(t *testing.T) {
	c := New(80, 24)

	_, _ = c.Write([]byte("printed before anyone was watching\n"))

	v, picture, cols, rows := c.Join()
	defer c.Leave(v)

	if !bytes.Contains(picture, []byte("printed before anyone was watching")) {
		t.Fatalf("what was on the screen is missing: %q", picture)
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
	_, _ = c.Write(buf)
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
		_, _ = c.Write([]byte("flood"))
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
		_, _ = c.Write([]byte("flood"))
	}
	// Already dropped for lagging; Leave must not close the channel a second time.
	c.Leave(v)
	c.Leave(v)
}

// What a watcher is handed on joining is a screen, not a log. However long a terminal has been
// running, the picture is one screenful.
func TestWhatAWatcherJoinsWithIsOneScreen(t *testing.T) {
	c := New(80, 24)

	chunk := bytes.Repeat([]byte{'x'}, 8<<10)
	for i := 0; i < 40; i++ {
		_, _ = c.Write(chunk)
	}

	_, picture, _, _ := c.Join()

	// A screen of 80x24 with escapes around it, not the 320KB that was written.
	if len(picture) > 64<<10 {
		t.Fatalf("a watcher was handed %d bytes to render one screen", len(picture))
	}
	if len(picture) == 0 {
		t.Fatal("a watcher was handed nothing at all")
	}
}

// A program that draws by moving the cursor is the case replaying bytes cannot serve: what matters
// is where the cursor left things, not which bytes went past lately.
func TestAWatcherJoiningSeesWhatIsOnTheScreen(t *testing.T) {
	c := New(80, 24)

	// Drawn once, long ago, and never sent again — exactly what a full-screen program does.
	_, _ = c.Write([]byte("\x1b[5;10Hlong gone from any tail"))

	// Then a great deal of unrelated traffic somewhere else on the screen.
	for i := 0; i < 200; i++ {
		_, _ = c.Write([]byte("\x1b[20;1Hbusy"))
	}

	_, picture, _, _ := c.Join()
	if !bytes.Contains(picture, []byte("long gone from any tail")) {
		t.Error("what was drawn once is missing from what a watcher joins with")
	}
	if !bytes.Contains(picture, []byte("busy")) {
		t.Error("what was drawn last is missing too")
	}
}

// A prompt that has been cleared is not handed to whoever joins next.
func TestClearingLeavesNothingForTheNextWatcher(t *testing.T) {
	c := New(80, 24)
	_, _ = c.Write([]byte("Password:"))

	c.Clear()

	_, picture, _, _ := c.Join()
	if bytes.Contains(picture, []byte("Password")) {
		t.Error("a cleared prompt was handed to the next watcher")
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

func TestDimensionsAreBoundedToTheScreen(t *testing.T) {
	c := New(1<<20, 1<<20)

	if cols, rows := c.Size(); cols != 1000 || rows != 1000 {
		t.Fatalf("new cast size = %dx%d, want 1000x1000", cols, rows)
	}

	c.Resize(65535, 65535)
	_, _, cols, rows := c.Join()
	if cols != 1000 || rows != 1000 {
		t.Fatalf("resized cast = %dx%d, want 1000x1000", cols, rows)
	}
}

// Joining a cast that has already stopped hands back a feed nothing will ever close, unless the
// caster says the cast is over — and then whoever is watching reads nothing and finishes.
func TestJoiningAfterTheCastEndedIsOver(t *testing.T) {
	c := New(80, 24)
	c.Stop()

	v, picture, cols, rows := c.Join()
	if len(picture) != 0 {
		t.Errorf("a cast that ended handed out a screen: %q", picture)
	}
	if cols != 80 || rows != 24 {
		t.Errorf("size came back as %dx%d, want 80x24", cols, rows)
	}

	select {
	case _, open := <-v.Frames():
		if open {
			t.Fatal("a cast that ended handed out a feed with something on it")
		}
	case <-time.After(time.Second):
		t.Fatal("a watcher joining a stopped cast waits for a channel nobody closes")
	}

	// And leaving is still safe, though it was never in the list.
	c.Leave(v)
}

// Nothing written or resized after the cast is over goes anywhere.
func TestWritingAfterTheCastEndedGoesNowhere(t *testing.T) {
	c := New(80, 24)
	c.Stop()

	if n, err := c.Write([]byte("too late")); n != len("too late") || err != nil {
		t.Fatalf("Write() after Stop came back %d, %v", n, err)
	}
	c.Resize(120, 40)

	if cols, rows := c.Size(); cols != 80 || rows != 24 {
		t.Errorf("a stopped cast resized to %dx%d", cols, rows)
	}
	if _, picture, _, _ := c.Join(); len(picture) != 0 {
		t.Errorf("what was written after the cast ended is on the screen: %q", picture)
	}
}

// A terminal has one shape at a time, and every watcher has to hear when it changes — the one who
// asked for the change as much as anybody.
func TestEveryWatcherHearsTheNewShape(t *testing.T) {
	c := New(80, 24)
	one, _, _, _ := c.Join()
	two, _, _, _ := c.Join()

	c.Resize(132, 40)

	for i, v := range []*Viewer{one, two} {
		select {
		case <-v.Changed():
		default:
			t.Fatalf("watcher %d was not told the terminal changed shape", i+1)
		}
	}
	if cols, rows := c.Size(); cols != 132 || rows != 40 {
		t.Fatalf("the terminal is %dx%d, not the shape it was given", cols, rows)
	}
}

// A terminal that follows its watchers is as big as the smallest window watching it, so everybody
// sees all of it; when the small window goes, it grows to the next smallest.
func TestTheSmallestWindowSetsTheShape(t *testing.T) {
	c := New(80, 24)
	var applied [][2]uint16
	c.Follow(func(cols, rows uint16) { applied = append(applied, [2]uint16{cols, rows}) })

	big, _, _, _ := c.Join()
	small, _, _, _ := c.Join()
	c.Want(big, 200, 50)
	c.Want(small, 90, 40)

	if cols, rows := c.Size(); cols != 90 || rows != 40 {
		t.Fatalf("the terminal is %dx%d with a 200x50 and a 90x40 window on it", cols, rows)
	}

	c.Leave(small)
	if cols, rows := c.Size(); cols != 200 || rows != 50 {
		t.Fatalf("the terminal stayed %dx%d after the small window left", cols, rows)
	}
	if last := applied[len(applied)-1]; last != [2]uint16{200, 50} {
		t.Fatalf("the program behind it was last told %v", last)
	}
}

// A recording has the shape it was made at: somebody's window is not a reason to change it.
func TestARecordingKeepsItsShape(t *testing.T) {
	c := New(100, 30)
	v, _, _, _ := c.Join()
	c.Want(v, 40, 10)

	if cols, rows := c.Size(); cols != 100 || rows != 30 {
		t.Fatalf("a recording was reshaped to %dx%d", cols, rows)
	}
}

// Who is watching is part of what a watcher is told: somebody joining or leaving is a change.
func TestJoiningAndLeavingAreChanges(t *testing.T) {
	c := New(80, 24)
	first, _, _, _ := c.Join()
	<-first.Changed()

	second, _, _, _ := c.Join()
	select {
	case <-first.Changed():
	default:
		t.Fatal("a watcher was not told somebody joined")
	}

	c.Leave(second)
	select {
	case <-first.Changed():
	default:
		t.Fatal("a watcher was not told somebody left")
	}
	if c.Watching() != 1 {
		t.Fatalf("%d watching after one left", c.Watching())
	}
}

// Shared, a terminal is not held below the size terminals are made at: the small window crops
// rather than taking everybody else's with it. Watched by one, it is that one window's.
func TestOneSmallWindowDoesNotShrinkItForEverybody(t *testing.T) {
	c := New(80, 24)
	c.Follow(func(uint16, uint16) {})

	phone, _, _, _ := c.Join()
	c.Want(phone, 50, 30)
	if cols, rows := c.Size(); cols != 50 || rows != 30 {
		t.Fatalf("watched by one window it is %dx%d, not that window's 50x30", cols, rows)
	}

	desk, _, _, _ := c.Join()
	c.Want(desk, 160, 45)
	if cols, rows := c.Size(); cols != leastCols || rows != 30 {
		t.Fatalf("shared by 50x30 and 160x45 it is %dx%d, want %dx30", cols, rows, leastCols)
	}
}
