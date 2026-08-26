package cmd

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/asciicast"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/ns"
)

// castStarted is a cast whose header has arrived and which then says nothing, which is what a
// terminal nobody is typing at looks like.
func castStarted(t *testing.T) *asciicast.Reader {
	t.Helper()

	in, into := io.Pipe()
	t.Cleanup(func() { _ = into.Close() })

	go func() {
		_, _ = io.WriteString(into, `{"version": 2, "width": 80, "height": 24}`+"\n")
	}()

	reader, head, err := asciicast.NewReader(in)
	if err != nil {
		t.Fatalf("asciicast.NewReader(): %v", err)
	}
	if head.Width != 80 {
		t.Fatalf("the header came out %dx%d", head.Width, head.Height)
	}
	return reader
}

// A cast waiting on standard input has to be interruptible: the read cannot be cancelled, so what
// waits on it must not be the thing that notices a signal.
func TestACastStopsBeingAskedThoughInputNeverEnds(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())

	stopped := make(chan error, 1)
	go func() { stopped <- pump(ctx, castStarted(t), cast.New(80, 24)) }()

	// Long enough to be blocked in the read rather than still starting up.
	time.Sleep(50 * time.Millisecond)
	stop()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("an interrupted cast came back with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cast blocked on standard input could not be interrupted")
	}
}

// One cast at a time. A second one behind the same path is two screens at one address, and whoever
// is watching cannot tell which they were given.
func TestASecondCastIsRefused(t *testing.T) {
	host := newCastHost(ns.NewTable())

	stage, err := host.begin(80, 24)
	if err != nil {
		t.Fatalf("the first cast was refused: %v", err)
	}

	if _, err := host.begin(120, 40); err == nil {
		t.Fatal("a second cast was put on the air over the first")
	}
	if host.live() != stage {
		t.Fatal("the running cast was replaced by one that was refused")
	}
}

// A cast that has already ended must not take down the one running now.
func TestAnEndedCastDoesNotEndTheNextOne(t *testing.T) {
	host := newCastHost(ns.NewTable())

	first, err := host.begin(80, 24)
	if err != nil {
		t.Fatalf("begin(): %v", err)
	}
	host.end(first)

	second, err := host.begin(120, 40)
	if err != nil {
		t.Fatalf("begin(): %v", err)
	}

	// The older one ending again, which is what a slow teardown looks like.
	host.end(first)

	if host.live() != second {
		t.Fatal("an older cast ending took the running one off the air")
	}
	if _, _, ok := host.mounts.Lookup(CastPath); !ok {
		t.Fatal("an older cast ending took the path with it")
	}
}

// A /cast written down in the config carries somebody's own access rule, and a cast coming and
// going must neither replace it nor take the path away.
func TestADeclaredCastPathKeepsItsRule(t *testing.T) {
	table := ns.NewTable()
	if err := table.Add(ns.Mount{
		Path:      CastPath,
		Archetype: ns.TTY,
		Access:    ns.Access{Named: []string{"bob"}},
	}); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	host := newCastHost(table)
	stage, err := host.begin(80, 24)
	if err != nil {
		t.Fatalf("begin(): %v", err)
	}

	mount, _, ok := table.Lookup(CastPath)
	if !ok || mount.Access.AnyPaired || len(mount.Access.Named) != 1 {
		t.Fatalf("a cast rewrote the rule on a declared path: %+v", mount.Access)
	}

	host.end(stage)
	if _, _, ok := table.Lookup(CastPath); !ok {
		t.Fatal("a cast ending took away a path the config declared")
	}
}

// A watcher that stops reading takes nothing, and the write to it never returns. The shell behind
// it is shared, so that watcher is given up on.
func TestAWatcherThatStopsReadingIsDropped(t *testing.T) {
	stuck := &stopped{let: make(chan struct{})}
	defer close(stuck.let)

	writing := pacing(stuck)
	if err := writing.write([]byte("anything"), 50*time.Millisecond); err != errStalled {
		t.Fatalf("a write that went nowhere came back with %v", err)
	}
}

// And one that is being read lands, in order.
func TestAWatcherThatReadsIsWrittenTo(t *testing.T) {
	var got taken

	writing := pacing(&got)
	for _, chunk := range []string{"one ", "two ", "three"} {
		if err := writing.write([]byte(chunk), time.Second); err != nil {
			t.Fatalf("writing %q: %v", chunk, err)
		}
	}

	if string(got.got) != "one two three" {
		t.Fatalf("the watcher saw %q", got.got)
	}
}

// taken keeps whatever was written to it.
type taken struct{ got []byte }

func (t *taken) Write(p []byte) (int, error) {
	t.got = append(t.got, p...)
	return len(p), nil
}

// stopped takes nothing until it is let go, which is what a window that stopped reading looks like
// from this end.
type stopped struct{ let chan struct{} }

func (s *stopped) Write(p []byte) (int, error) {
	<-s.let
	return len(p), nil
}
