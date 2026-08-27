package stream

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/wire"
)

// A command's output comes through a pipe, where nothing translates line endings. Drawn on a
// terminal screen at the far end, a bare line feed moves down without moving back, and every line
// starts further right than the one before it.
func TestAStreamsNewlinesBecomeTerminalOnes(t *testing.T) {
	var got strings.Builder

	if _, err := (asTerminal{&got}).Write([]byte("one\ntwo\n")); err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if got.String() != "one\r\ntwo\r\n" {
		t.Errorf("wrote %q", got.String())
	}
}

// A command that already writes CRLF must not end up with two carriage returns.
func TestAlreadyTerminalNewlinesAreLeftAlone(t *testing.T) {
	var got strings.Builder

	if _, err := (asTerminal{&got}).Write([]byte("one\r\ntwo\r\n")); err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if got.String() != "one\r\ntwo\r\n" {
		t.Errorf("wrote %q", got.String())
	}
}

// The count returned is what the caller handed over, not what was written: io.Copy treats a short
// write as an error, and every translated newline makes the write longer than the read.
func TestTheTranslatorReportsWhatItWasGiven(t *testing.T) {
	var got strings.Builder

	n, err := (asTerminal{&got}).Write([]byte("one\ntwo\n"))
	if err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if n != len("one\ntwo\n") {
		t.Errorf("reported %d bytes, was given %d", n, len("one\ntwo\n"))
	}
}

// A command that has said everything it is going to say ends the session. Waiting on a far end that
// opened the stream and then went quiet holds this goroutine, and the command it started with it,
// for as long as that peer keeps the connection.
func TestAStreamEndsWhenItsCommandDoes(t *testing.T) {
	s := newQuiet()

	done := make(chan error, 1)
	go func() {
		done <- (&Stream{}).Serve(context.Background(), arch.Session{
			Path:   "/log",
			Config: Config{Command: "echo hello"},
			Conn:   wire.NewConn(s),
			Stream: s,
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve(): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve() stayed on a far end that had nothing to say")
	}

	if got := s.written(); !strings.Contains(got, "hello") {
		t.Fatalf("the far end was sent %q", got)
	}
}

// quiet is a stream nobody is saying anything on: writes land, reads wait, and a read deadline in
// the past is what ends a read already in flight.
type quiet struct {
	mu   sync.Mutex
	past bool
	wake chan struct{}
	sent strings.Builder
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

func (q *quiet) written() string {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.sent.String()
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

// A command that leaves something behind must not leave the session behind with it.
//
// The shell is what gets cancelled, and a command that puts something in the background, or pipes,
// or otherwise does not exec into a single process, leaves a grandchild holding the write end of
// the output pipe. Reading that pipe then waits on something that will never finish — not when the
// peer hangs up, not when the session ends, not when the daemon stops. One goroutine, one stream,
// one pipe and one process, kept for good, as many times as a peer cares to open the namespace.
func TestAStreamDoesNotOutliveItsSession(t *testing.T) {
	s := newQuiet()
	ctx, stop := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- (&Stream{}).Serve(ctx, arch.Session{
			Path: "/log",
			// Backgrounded, so the shell exits and something else keeps the pipe.
			Config: Config{Command: "sleep 600 & echo up"},
			Conn:   wire.NewConn(s),
			Stream: s,
		})
	}()

	// Wait until the command has actually said something, so the session is properly under way.
	for range 100 {
		if strings.Contains(s.written(), "up") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	stop()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Serve() outlived its session: a peer can leave one of these behind every time it opens the namespace")
	}
}
