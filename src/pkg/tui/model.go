package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/charmbracelet/bubbles/list"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/proto"
	tickets "github.com/bresilla/drop/src/pkg/ticket"
	"io"
)

// Backend is what the interface needs from the rest of drop. An interface rather than the node
// itself, so the panes can be driven by a fake and tested without a network.
type Backend interface {
	// Self is this device, for the header.
	Self() (Identity, error)
	// Peers is the address book.
	Peers() ([]book.Entry, error)
	// Serves asks a device what it shares with us.
	Serves(ctx context.Context, with book.Entry) ([]proto.Served, error)
	// History is a conversation as it stands.
	History(with book.Entry) ([]convo.Message, error)
	// Say sends a message.
	Say(ctx context.Context, to book.Entry, body string) error
	// Offer puts this device up for pairing and reports the name it paired with. The ticket comes
	// back at once so it can be shown; the channel yields once the far end has finished.
	Offer(ctx context.Context) (ticket string, done <-chan string, err error)

	// Join takes a ticket somebody else is showing, which is the other half of pairing.
	Join(ctx context.Context, ticket string) (with string, err error)

	// Send copies files to a path on another device, reporting progress as it goes.
	Send(ctx context.Context, to book.Entry, path string, files []string, progress func(name string, done, size int64)) error

	// Post sends one message to a path: a line of text to a chat, a URL to a link.
	Post(ctx context.Context, to book.Entry, path string, kind byte, body string) error

	// Watch reads a live path, writing what arrives into screen until ctx ends.
	Watch(ctx context.Context, on book.Entry, path string, into io.Writer, resize func(cols, rows int)) error
}

// level is how deep you have gone. Entering rather than tabbing: what a path is depends on the
// device it is on, so the two are a sequence rather than two columns to compare.
type level int

const (
	levelDevices level = iota
	levelPaths
	levelOpen
)

// Model is the whole interface.
type Model struct {
	back Backend

	at     level
	list   list.Model
	width  int
	height int

	// me is this device, shown in the header: two of these side by side are two machines.
	me Identity

	// linking is a pairing being offered, shown until it finishes or is abandoned.
	linking *pairing
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
	// joining is a ticket being typed in, which is the other half of pairing: one device
	// shows a code, the other takes it.
	joining bool

	// putting is a file path or a URL being typed, to send to the open path. offering is what
	// is moving while it moves, and options are the completions last offered for a path.
	putting  bool
	offering *moving
	options  []string
	said     string

	typing  string
	writing bool
}

// New builds the interface over a backend.
func New(back Backend) Model {
	shown := list.New(nil, rows{}, 0, 0)
	shown.SetShowTitle(false)
	shown.SetShowHelp(false)
	shown.SetShowStatusBar(false)
	shown.SetFilteringEnabled(true)
	shown.SetShowPagination(true)

	return Model{back: back, at: levelDevices, list: shown}
}

func (m Model) Init() tea.Cmd { return tea.Batch(loadSelf(m.back), loadPeers(m.back)) }

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

// Reaching costs a lookup and a dial, so it gets a deadline: without one an unreachable device
// leaves the pane saying "asking…" for as long as the program is open, with nothing to act on.
const askFor = 30 * time.Second

func loadPaths(back Backend, with book.Entry) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), askFor)
		defer cancel()

		paths, err := back.Serves(ctx, with)
		if err != nil && ctx.Err() != nil {
			err = fmt.Errorf("%s did not answer within %s", with.Name, askFor)
		}
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

// The list is the page: full width, with room left for the header and the keys.
// The room a list has, inside the panel it is drawn in: the header and its rule, the footer, and
// the panel's own top and bottom edges.
func (m Model) listHeight() int {
	got := m.height - 5
	if got < rowHeight {
		return rowHeight
	}
	return got
}

// listWidth is the same for columns: the panel's borders and the padding inside them.
func (m Model) listWidth() int {
	got := m.width - 4
	if got < 20 {
		return 20
	}
	return got
}

func (m Model) viewWidth() int {
	if m.width < 20 {
		return 20
	}
	return m.width - 2
}

func (m Model) viewHeight() int {
	got := m.height - 5
	if got < 4 {
		return 4
	}
	return got
}

// Identity is this device, as the header shows it.
type Identity struct {
	Name string
	ID   string
}

type selfLoaded struct {
	me  Identity
	err error
}

func loadSelf(back Backend) tea.Cmd {
	return func() tea.Msg {
		me, err := back.Self()
		return selfLoaded{me: me, err: err}
	}
}

// pairing is what the interface shows while a device is being linked.
type pairing struct {
	ticket string
	code   string
	waited <-chan string
	stop   context.CancelFunc
}

type pairStarted struct {
	at  *pairing
	err error
}

type pairDone struct{ with string }

// offer puts this device up for pairing.
func offer(back Backend) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithCancel(context.Background())

		ticket, waited, err := back.Offer(ctx)
		if err != nil {
			stop()
			return pairStarted{err: err}
		}

		at := &pairing{ticket: ticket, waited: waited, stop: stop}

		// The code is drawn rather than the ticket alone: a phone has no keyboard worth typing a
		// hundred characters on, and a camera is the whole point of showing one.
		if drawn, err := tickets.Code(ticket); err == nil {
			at.code = tickets.Render(drawn)
		}
		return pairStarted{at: at}
	}
}

// waitForPair blocks until the far end finishes, so the list can be reloaded the moment it does.
func waitForPair(waited <-chan string) tea.Cmd {
	if waited == nil {
		return nil
	}
	return func() tea.Msg {
		with, ok := <-waited
		if !ok {
			return nil
		}
		return pairDone{with: with}
	}
}

type joined struct {
	with string
	err  error
}

// join takes a ticket another device is showing.
func join(back Backend, ticket string) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()

		with, err := back.Join(ctx, ticket)
		return joined{with: with, err: err}
	}
}
