package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/proto"
	"io"
)

// Backend is what the interface needs from the rest of drop. An interface rather than the node
// itself, so the panes can be driven by a fake and tested without a network.
type Backend interface {
	// Peers is the address book.
	Peers() ([]book.Entry, error)
	// Serves asks a device what it shares with us.
	Serves(ctx context.Context, with book.Entry) ([]proto.Served, error)
	// History is a conversation as it stands.
	History(with book.Entry) ([]convo.Message, error)
	// Say sends a message.
	Say(ctx context.Context, to book.Entry, body string) error
	// Watch reads a live path, writing what arrives into screen until ctx ends.
	Watch(ctx context.Context, on book.Entry, path string, into io.Writer, resize func(cols, rows int)) error
}

// pane is which column the keys reach.
type pane int

const (
	panePeers pane = iota
	panePaths
	paneView
)

// Model is the whole interface.
type Model struct {
	back Backend

	focus  pane
	width  int
	height int

	peers   []book.Entry
	atPeer  int
	paths   []proto.Served
	atPath  int
	loading bool
	trouble string

	// history is the conversation being shown, when the open path is a chat.
	history []convo.Message
	// screen is the far end's terminal, when the open path is live.
	screen  *screen
	live    bool
	stopped context.CancelFunc

	// typing is the message being composed, and whether the view pane is taking keys for it.
	typing  string
	writing bool
}

// New builds the interface over a backend.
func New(back Backend) Model {
	return Model{back: back, focus: panePeers}
}

func (m Model) Init() tea.Cmd { return loadPeers(m.back) }

// ---------------------------------------------------------------- what arrives

type peersLoaded struct {
	peers []book.Entry
	err   error
}

type pathsLoaded struct {
	peer  string
	paths []proto.Served
	err   error
}

type historyLoaded struct {
	peer string
	log  []convo.Message
	err  error
}

// framePainted says the far end's terminal has changed and the view should be redrawn. The screen
// itself is shared, so nothing about it travels in the message.
type framePainted struct{}

type watchEnded struct{ err error }

type saidIt struct{ err error }

func loadPeers(back Backend) tea.Cmd {
	return func() tea.Msg {
		peers, err := back.Peers()
		return peersLoaded{peers: peers, err: err}
	}
}

func loadPaths(back Backend, with book.Entry) tea.Cmd {
	return func() tea.Msg {
		paths, err := back.Serves(context.Background(), with)
		return pathsLoaded{peer: with.Name, paths: paths, err: err}
	}
}

func loadHistory(back Backend, with book.Entry) tea.Cmd {
	return func() tea.Msg {
		log, err := back.History(with)
		return historyLoaded{peer: with.Name, log: log, err: err}
	}
}

// ---------------------------------------------------------------- helpers

func (m Model) peer() (book.Entry, bool) {
	if m.atPeer < 0 || m.atPeer >= len(m.peers) {
		return book.Entry{}, false
	}
	return m.peers[m.atPeer], true
}

func (m Model) path() (proto.Served, bool) {
	if m.atPath < 0 || m.atPath >= len(m.paths) {
		return proto.Served{}, false
	}
	return m.paths[m.atPath], true
}

// stop ends whatever is being watched, so moving away from a terminal does not leave one being read
// by nobody.
func (m *Model) stop() {
	if m.stopped != nil {
		m.stopped()
		m.stopped = nil
	}
	m.live = false
	m.screen = nil
}

func kindOf(s proto.Served) string { return s.Kind.String() }

func shortID(e book.Entry) string {
	id := e.ID.String()
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func lines(text string, n int) string {
	got := strings.Split(text, "\n")
	if len(got) <= n {
		return text
	}
	return strings.Join(got[len(got)-n:], "\n")
}

func joinPanes(width int, panes ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, panes...)
}

var _ = fmt.Sprintf

// say puts a message on the wire.
func say(back Backend, to book.Entry, body string) tea.Cmd {
	return func() tea.Msg {
		return saidIt{err: back.Say(context.Background(), to, body)}
	}
}

// watch reads a live path into a screen until the context ends.
//
// The screen is written from this goroutine and read from the interface's, which is why the nudge
// carries nothing: by the time the interface reacts, what it reads is whatever is there now.
func watch(back Backend, on book.Entry, path string, into *screen, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		err := back.Watch(ctx, on, path, into, into.Resize)
		close(into.nudge)
		return watchEnded{err: err}
	}
}

// waitForFrame blocks until the far end has drawn something, so the interface repaints when there is
// a reason to rather than on a timer.
func waitForFrame(nudge chan struct{}) tea.Cmd {
	if nudge == nil {
		return nil
	}
	return func() tea.Msg {
		if _, ok := <-nudge; !ok {
			return nil
		}
		return framePainted{}
	}
}

// The three panes split the width: two narrow lists and whatever is left for the content.
func (m Model) listWidth() int {
	if m.width < 60 {
		return 16
	}
	return min(26, m.width/5)
}

func (m Model) viewWidth() int {
	got := m.width - 2*m.listWidth() - 10
	if got < 20 {
		return 20
	}
	return got
}

func (m Model) viewHeight() int {
	got := m.height - 6
	if got < 5 {
		return 5
	}
	return got
}
