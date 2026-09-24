package cmd

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

type cancelableStream struct {
	readOnce  sync.Once
	writeOnce sync.Once
	closeOnce sync.Once
	read      chan struct{}
	write     chan struct{}
	closed    chan struct{}
}

func newCancelableStream() *cancelableStream {
	return &cancelableStream{
		read:   make(chan struct{}),
		write:  make(chan struct{}),
		closed: make(chan struct{}),
	}
}

func (s *cancelableStream) Read([]byte) (int, error) {
	<-s.read
	return 0, os.ErrDeadlineExceeded
}

func (s *cancelableStream) Write([]byte) (int, error) {
	<-s.write
	return 0, os.ErrDeadlineExceeded
}

func (s *cancelableStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *cancelableStream) SetReadDeadline(at time.Time) error {
	if !at.IsZero() && !at.After(time.Now()) {
		s.readOnce.Do(func() { close(s.read) })
	}
	return nil
}

func (s *cancelableStream) SetWriteDeadline(at time.Time) error {
	if !at.IsZero() && !at.After(time.Now()) {
		s.writeOnce.Do(func() { close(s.write) })
	}
	return nil
}

func TestAContextEndsBothStreamDirections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newCancelableStream()
	stop := stopStreamOnDone(ctx, s)
	defer stop()

	readDone := make(chan struct{})
	writeDone := make(chan struct{})
	go func() { _, _ = s.Read(nil); close(readDone) }()
	go func() { _, _ = s.Write(nil); close(writeDone) }()
	cancel()

	for name, done := range map[string]<-chan struct{}{
		"read": readDone, "write": writeDone, "close": s.closed,
	} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("the %s side outlived its context", name)
		}
	}
}

func TestACompletedOperationDisarmsStreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newCancelableStream()
	stopStreamOnDone(ctx, s)()
	cancel()

	select {
	case <-s.closed:
		t.Fatal("a completed operation was closed by its old context")
	case <-time.After(20 * time.Millisecond):
	}
}
