package proto

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/wire"
)

// watched is a session stream that remembers the first deadline put on its read side.
type watched struct {
	net.Conn

	mu    sync.Mutex
	first time.Time
	seen  bool
}

func (w *watched) SetReadDeadline(t time.Time) error {
	w.mu.Lock()
	if !w.seen && !t.IsZero() {
		w.first, w.seen = t, true
	}
	w.mu.Unlock()
	return w.Conn.SetReadDeadline(t)
}

func (w *watched) bounded() (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.first, w.seen
}

// The side that speaks first bounds the answer the same way the side listening bounds the opening.
// A peer that takes the stream and says nothing otherwise holds the goroutine and the stream that
// opened it until the process stops.
func TestAnOpeningIsBoundedWhileItWaitsToBeAnswered(t *testing.T) {
	caller, server := net.Pipe()
	t.Cleanup(func() { caller.Close() })

	go func() {
		defer server.Close()
		c := wire.NewConn(server)
		if _, _, err := c.ReadFrame(); err != nil {
			return
		}
		_ = c.WriteFrame(wire.KindAccept, nil)
	}()

	w := &watched{Conn: caller}
	if _, err := Open(w, "/notes", "", 0, "", "tester"); err != nil {
		t.Fatalf("Open(): %v", err)
	}

	at, bounded := w.bounded()
	if !bounded {
		t.Fatal("nothing bounds how long an opening waits to be answered")
	}
	if at.Before(time.Now()) {
		t.Fatalf("the answer was bounded to %v, which is already past", at)
	}
}

// A stream that was accepted goes on for as long as what is said on it takes, so the bound on the
// opening is lifted once there is an answer.
func TestAnAcceptedSessionIsNotBounded(t *testing.T) {
	caller, server := net.Pipe()
	t.Cleanup(func() { caller.Close() })

	go func() {
		defer server.Close()
		c := wire.NewConn(server)
		if _, _, err := c.ReadFrame(); err != nil {
			return
		}
		_ = c.WriteFrame(wire.KindAccept, nil)
		time.Sleep(20 * time.Millisecond)
		_ = c.WriteFrame(wire.KindItem, []byte("late"))
	}()

	conn, err := Open(stream{caller}, "/notes", "", 0, "", "tester")
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if _, body, err := conn.ReadFrame(); err != nil {
		t.Fatalf("reading what was said afterwards: %v", err)
	} else if string(body) != "late" {
		t.Fatalf("read %q", body)
	}
}
