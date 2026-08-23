package tui

import (
	"sync"

	"github.com/bresilla/drop/src/pkg/term"
)

// screen is a terminal being read from one goroutine and drawn from another.
//
// term.Screen has no lock of its own, because everywhere else it is used by one goroutine at a time.
// Here the network writes into it while the interface reads it, so the lock lives at this boundary
// rather than being paid for by every other user.
type screen struct {
	mu    sync.Mutex
	inner *term.Screen

	// nudge tells the interface a repaint is worth doing. Depth one, and a full channel is simply
	// left alone: the signal carries nothing, so one pending nudge means the same as ten.
	nudge chan struct{}
}

func newScreen(cols, rows int) *screen {
	return &screen{inner: term.New(cols, rows), nudge: make(chan struct{}, 1)}
}

func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	n, err := s.inner.Write(p)
	s.mu.Unlock()

	s.wake()
	return n, err
}

func (s *screen) Resize(cols, rows int) {
	s.mu.Lock()
	s.inner.Resize(cols, rows)
	s.mu.Unlock()

	s.wake()
}

// Draw is the picture as it stands.
func (s *screen) Draw() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.inner.ANSI()
}

func (s *screen) wake() {
	select {
	case s.nudge <- struct{}{}:
	default:
	}
}
