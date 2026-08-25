package tui

import (
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

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
	//
	// Never closed. Whatever is reading the far end goes on writing for a moment after the watch
	// is over -- the read that was already in flight has to land somewhere -- and closing this
	// would turn that into a panic on a channel nobody owns any more.
	nudge chan struct{}
	// done says the screen is finished with, so whoever is waiting for a repaint stops waiting.
	done chan struct{}
	over sync.Once
}

func newScreen(cols, rows int) *screen {
	return &screen{
		inner: term.New(cols, rows),
		nudge: make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
}

// Finish says nothing more will be drawn from this screen. Safe to call more than once, and safe
// while somebody is still writing to it.
func (s *screen) Finish() {
	s.over.Do(func() { close(s.done) })
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
	case <-s.done:
		return
	default:
	}

	select {
	case s.nudge <- struct{}{}:
	default:
	}
}

// keyBytes is a keypress as the bytes a terminal expects.
//
// Bubbletea has already decoded the escape sequences into keys, so this puts back the ones a pty
// on the far end is waiting for. Runes go as themselves; the rest are what a terminal sends.
func keyBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	}

	// The control keys are their letter minus sixty-four: ctrl+c is 3, ctrl+d is 4, and so on.
	if name := msg.String(); strings.HasPrefix(name, "ctrl+") && len(name) == 6 {
		if c := name[5]; c >= 'a' && c <= 'z' {
			return []byte{c - 'a' + 1}
		}
	}
	return nil
}
