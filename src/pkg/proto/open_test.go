package proto

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/wire"
)

// watched is a session stream that remembers the deadlines put on both sides.
type watched struct {
	net.Conn

	mu           sync.Mutex
	readFirst    time.Time
	writeFirst   time.Time
	readSeen     bool
	writeSeen    bool
	readCleared  bool
	writeCleared bool
}

func (w *watched) SetReadDeadline(t time.Time) error {
	w.mu.Lock()
	if t.IsZero() {
		w.readCleared = true
	} else if !w.readSeen {
		w.readFirst, w.readSeen = t, true
	}
	w.mu.Unlock()
	return w.Conn.SetReadDeadline(t)
}

func (w *watched) SetWriteDeadline(t time.Time) error {
	w.mu.Lock()
	if t.IsZero() {
		w.writeCleared = true
	} else if !w.writeSeen {
		w.writeFirst, w.writeSeen = t, true
	}
	w.mu.Unlock()
	return w.Conn.SetWriteDeadline(t)
}

func (w *watched) bounded() (time.Time, time.Time, bool, bool, bool, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.readFirst, w.writeFirst, w.readSeen, w.writeSeen, w.readCleared, w.writeCleared
}

// Both sides of an opening are bounded, and an accepted session has both bounds removed.
func TestAnOpeningBoundsAndThenClearsBothSides(t *testing.T) {
	caller, server := net.Pipe()
	t.Cleanup(func() { _ = caller.Close() })
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	go func() {
		defer func() { _ = server.Close() }()
		c := wire.NewConn(server)
		if _, _, err := c.ReadFrame(); err != nil {
			return
		}
		_ = c.WriteFrame(wire.KindAccept, nil)
		<-release
	}()

	w := &watched{Conn: caller}
	if _, err := Open(w, "/notes", "", 0, "", "tester"); err != nil {
		t.Fatalf("Open(): %v", err)
	}

	readAt, writeAt, readSeen, writeSeen, readCleared, writeCleared := w.bounded()
	if !readSeen || !writeSeen {
		t.Fatalf("opening deadlines set on read=%t write=%t", readSeen, writeSeen)
	}
	if readAt.Before(time.Now()) || writeAt.Before(time.Now()) {
		t.Fatalf("opening deadlines are already past: read=%v write=%v", readAt, writeAt)
	}
	if !readCleared || !writeCleared {
		t.Fatalf("accepted session deadlines cleared on read=%t write=%t", readCleared, writeCleared)
	}
}

// A stream that was accepted goes on for as long as what is said on it takes, so the bound on the
// opening is lifted once there is an answer.
func TestAnAcceptedSessionIsNotBounded(t *testing.T) {
	caller, server := net.Pipe()
	t.Cleanup(func() { _ = caller.Close() })

	go func() {
		defer func() { _ = server.Close() }()
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
