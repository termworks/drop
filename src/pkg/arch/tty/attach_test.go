package tty

import (
	"bytes"
	"io"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/live"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Either direction ending ends the session. A watcher whose feed has stopped — the shell it was
// watching is gone, or it fell too far behind — must not be left in the pump waiting on a peer that
// has no reason to say anything else.
func TestAnEndedFeedEndsTheAttach(t *testing.T) {
	stage := cast.New(80, 24)
	stage.Stop()

	s := newQuiet()
	d := live.New(wire.NewConn(s), s)

	done := make(chan error, 1)
	go func() { done <- attach(d, stage, io.Discard, nil) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("attach() came back with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attach() stayed in the pump after the watcher's feed had ended")
	}
}

// A watcher resolving a path while its shell is being reaped is handed whatever the map holds, so
// what the map holds has to still be a terminal somebody can be attached to.
func TestATerminalLeavesTheMapBeforeItIsTakenApart(t *testing.T) {
	tty := New(Into{})
	defer tty.Stop()

	term, err := tty.at("/shell", Config{Shell: "/bin/sh"})
	if err != nil {
		t.Fatalf("at(): %v", err)
	}

	// The map held the way a watcher holds it, and the shell ended underneath: the reaper gets as
	// far as it can while somebody could still be given this terminal.
	tty.mu.Lock()
	_, _ = term.ptmx.WriteString("exit\n")
	time.Sleep(200 * time.Millisecond)
	usable := alive(term.stage)
	tty.mu.Unlock()

	if !usable {
		t.Fatal("a terminal still in the map had already been stopped")
	}
}

// A daemon started by a service manager has no terminal type of its own, and a shell that inherits
// that has curses programs refusing to start.
func TestTheShellIsGivenATerminalType(t *testing.T) {
	t.Setenv("TERM", "")
	if env := environ(); !slices.Contains(env, "TERM=xterm-256color") {
		t.Error("a shell started where there is no TERM was not given one")
	}

	t.Setenv("TERM", "screen-256color")
	if env := environ(); !slices.Contains(env, "TERM=screen-256color") {
		t.Error("the terminal type this process has was not passed on")
	}
}

// alive reports whether a screen can still be watched: joining one that has stopped hands back a
// feed that is already closed.
func alive(stage *cast.Caster) bool {
	viewer, _, _, _ := stage.Join()
	defer stage.Leave(viewer)

	select {
	case <-viewer.Frames():
		return false
	default:
		return true
	}
}

// quiet is a stream nobody is saying anything on: writes land, reads wait, and a read deadline in
// the past is what ends a read already in flight.
type quiet struct {
	mu   sync.Mutex
	past bool
	wake chan struct{}
	sent bytes.Buffer
}

func newQuiet() *quiet { return &quiet{wake: make(chan struct{})} }

func (q *quiet) Read(p []byte) (int, error) {
	q.mu.Lock()
	wake, past := q.wake, q.past
	q.mu.Unlock()

	if !past {
		<-wake
	}
	return 0, os.ErrDeadlineExceeded
}

func (q *quiet) Write(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.sent.Write(p)
}

func (q *quiet) Close() error { return nil }

func (q *quiet) SetReadDeadline(t time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !t.IsZero() && !t.After(time.Now()) && !q.past {
		q.past = true
		close(q.wake)
	}
	return nil
}
