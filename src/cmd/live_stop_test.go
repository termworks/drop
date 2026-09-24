package cmd

import (
	stdbytes "bytes"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/live"
	"github.com/bresilla/drop/src/pkg/wire"
)

type halfClosedStream struct {
	mu       sync.Mutex
	wake     chan struct{}
	once     sync.Once
	deadline bool
	written  stdbytes.Buffer
}

func newHalfClosedStream() *halfClosedStream {
	return &halfClosedStream{wake: make(chan struct{})}
}

func (s *halfClosedStream) Read([]byte) (int, error) {
	<-s.wake
	return 0, os.ErrDeadlineExceeded
}

func (s *halfClosedStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written.Write(p)
}

func (s *halfClosedStream) Close() error { return nil }

func (s *halfClosedStream) SetReadDeadline(at time.Time) error {
	if !at.IsZero() && !at.After(time.Now()) {
		s.mu.Lock()
		s.deadline = true
		s.mu.Unlock()
		s.once.Do(func() { close(s.wake) })
	}
	return nil
}

func (s *halfClosedStream) SetWriteDeadline(time.Time) error { return nil }

func TestStoppingALiveSessionEndsItsReadPump(t *testing.T) {
	s := newHalfClosedStream()
	d := live.New(wire.NewConn(s), s)
	done := make(chan error, 1)
	go func() { done <- d.Pump(stdbytes.NewBuffer(nil)) }()

	started := time.Now()
	stopLive(d, done)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("stopping the live session took %v", elapsed)
	}

	s.mu.Lock()
	deadline := s.deadline
	s.mu.Unlock()
	if !deadline {
		t.Fatal("stopping the live session did not end its read side")
	}
}
