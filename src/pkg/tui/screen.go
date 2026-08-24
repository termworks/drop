package tui

import (
	"strings"
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

// Resize follows the far end's terminal, when it has one.
//
// A namespace that is a command rather than a pty has no size to report and says zero. Taking that
// literally leaves a grid with no cells in it, which swallows everything written afterwards and
// draws an empty screen that looks exactly like a stream sending nothing.
func (s *screen) Resize(cols, rows int) {
	if cols < 1 || rows < 1 {
		return
	}

	s.mu.Lock()
	s.inner.Resize(cols, rows)
	s.mu.Unlock()

	s.wake()
}

// Draw is the picture as it stands, as lines for a view to put inside a bigger one.
//
// Without the carriage returns. term.Screen ends a line with CRLF because its other reader writes
// straight to a terminal, where a bare newline leaves the cursor where it was. Here the lines are
// handed to a renderer that pads each one to the width of the window: a carriage return sends the
// cursor back to the start of the line it just wrote, and the padding then blanks it.
func (s *screen) Draw() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return strings.ReplaceAll(s.inner.ANSI(), "\r\n", "\n")
}

func (s *screen) wake() {
	select {
	case s.nudge <- struct{}{}:
	default:
	}
}
