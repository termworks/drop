package live

import (
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/wire"
)

// A far end that stops reading takes nothing, and the write to it never returns. What is behind it
// is shared with everybody else on it, so that end is given up on.
func TestAFarEndThatStopsReadingIsDropped(t *testing.T) {
	stuck := newStopped()

	writing := Pacing(New(wire.NewConn(stuck), stuck), 50*time.Millisecond)
	if _, err := writing.Write([]byte("anything")); err != ErrStalled {
		t.Fatalf("a write that went nowhere came back with %v", err)
	}
	if _, err := writing.Write([]byte("more")); err != ErrStalled {
		t.Fatalf("a write after the stall came back with %v", err)
	}
	select {
	case <-stuck.stopped:
	case <-time.After(time.Second):
		t.Fatal("the stalled transport write was not interrupted")
	}
	select {
	case <-writing.done:
	case <-time.After(time.Second):
		t.Fatal("the paced worker outlived its stalled write")
	}
}

// And one that is being read lands, in order.
func TestAFarEndThatReadsIsWrittenTo(t *testing.T) {
	got := taken{stopped: make(chan struct{})}

	writing := Pacing(&got, time.Second)

	for _, chunk := range []string{"one ", "two ", "three"} {
		if _, err := writing.Write([]byte(chunk)); err != nil {
			t.Fatalf("writing %q: %v", chunk, err)
		}
	}

	if string(got.got) != "one two three" {
		t.Fatalf("the far end saw %q", got.got)
	}
	writing.Give()
	select {
	case <-writing.done:
	case <-time.After(time.Second):
		t.Fatal("the paced worker did not stop after Give")
	}
	select {
	case <-got.stopped:
		t.Fatal("normal completion interrupted the transport write side")
	default:
	}
}

// taken keeps whatever was written to it.
type taken struct {
	got     []byte
	stopped chan struct{}
}

func (t *taken) Write(p []byte) (int, error) {
	t.got = append(t.got, p...)
	return len(p), nil
}

func (t *taken) StopWrite() { close(t.stopped) }

// stopped takes nothing until it is let go, which is what a peer that stopped reading looks like
// from this end.
type stopped struct {
	let     chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func newStopped() *stopped {
	return &stopped{let: make(chan struct{}), stopped: make(chan struct{})}
}

func (s *stopped) Write(p []byte) (int, error) {
	<-s.let
	return 0, os.ErrDeadlineExceeded
}

func (*stopped) Read([]byte) (int, error) { return 0, io.EOF }
func (*stopped) Close() error             { return nil }
func (*stopped) SetReadDeadline(time.Time) error {
	return nil
}

func (s *stopped) SetWriteDeadline(at time.Time) error {
	if !at.IsZero() && !at.After(time.Now()) {
		s.once.Do(func() {
			close(s.stopped)
			close(s.let)
		})
	}
	return nil
}
