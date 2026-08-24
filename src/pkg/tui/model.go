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
	"github.com/bresilla/drop/src/pkg/node"
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
	// Reaching is which devices a connection is being held to, by the name they are filed under.
	// Not a probe: it reports what is open, because dialling everybody to draw a list would spend
	// a handshake per device per redraw.
	Reaching() map[string]bool
	// Serves asks a device what it shares with us.
	Serves(ctx context.Context, with book.Entry) ([]proto.Served, error)
	// Mine is what this device serves, read from its own config rather than asked over a wire.
	Mine() ([]proto.Served, error)
	// Holding is what is in one of this machine's own files namespaces. Offering to send a file
	// to your own disk is not what that screen is for; saying what is in it is.
	Holding(path string) ([]Held, error)
	// Access is who may reach one of this machine's own paths: what the config says, and what
	// has been granted here.
	Access(path string) (Rule, error)
	// Grant lets somebody reach a path, Refuse stops them whatever else says otherwise, and
	// Unset leaves them to whatever the config says. All three write drop's own file, never the
	// config.
	Grant(path, who string) error
	Refuse(path, who string) error
	Unset(path, who string) error
	// History is a conversation as it stands.
	History(with book.Entry) ([]convo.Message, error)
	// Compose writes a message into the conversation without sending it. It returns as fast as a
	// disk write, because that is all it is.
	Compose(to book.Entry, body string) error
	// Deliver sends whatever is queued for a device.
	Deliver(ctx context.Context, to book.Entry) error
	// Waiting is which messages have not been acknowledged yet, by id.
	Waiting(with book.Entry) (map[string]bool, error)
	// Offer puts this device up for pairing and reports the name it paired with. The ticket comes
	// back at once so it can be shown; the channel yields once the far end has finished.
	Offer(ctx context.Context) (ticket string, done <-chan string, err error)

	// Join takes a ticket somebody else is showing, which is the other half of pairing.
	Join(ctx context.Context, ticket string) (with string, err error)

	// Send copies files to a path on another device, reporting progress as it goes.
	Send(ctx context.Context, to book.Entry, path string, files []string, progress func(name string, done, size int64)) error

	// Post sends one message to a path: a line of text to a chat, a URL to a link.
	Post(ctx context.Context, to book.Entry, path string, kind byte, body string) error

	// Arrivals reports when something lands from another device, so what is on screen is what
	// has happened rather than what had happened when it was last drawn.
	Arrivals() <-chan struct{}

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
	// levelAccess is who may reach one of this machine's own paths, and where that is changed.
	levelAccess
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
	// onSelf is true while the list is showing what this device serves rather than a peer's.
	onSelf bool
	paths  []proto.Served
	// rows is how the address book was arranged into the list: which row is which device.
	rows grouped
	// reaching is which devices a connection is being held to, by name.
	reaching map[string]bool
	// rule is who may reach the path whose access is being looked at.
	rule Rule
	// under is where in a device's paths the list is standing, "/" being the top.
	under string
	// steps is what is at that level: namespaces, and the ways further down.
	steps []step
	// known is what each device said it shares, kept from last time.
	//
	// Asking takes a round trip over somebody else's network. Without this the list empties the
	// moment a device is entered and fills again when the answer comes, which reads as the screen
	// losing its mind rather than as waiting.
	known   map[string][]proto.Served
	atPath  int
	loading bool
	trouble string

	// held is what is in one of this machine's own files namespaces, when one is open.
	held []Held
	// history is the conversation being shown, when the open path is a chat.
	history []convo.Message
	// waiting is which of those have not been acknowledged yet, by id.
	waiting map[string]bool
	// scroll is how many lines back from the newest the conversation is being read, so a person
	// looking at something older is not dragged to the bottom every time somebody says a word.
	scroll int
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

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadSelf(m.back), loadPeers(m.back), listenFor(m.back.Arrivals()))
}

// ---------------------------------------------------------------- what arrives

type peersLoaded struct {
	peers []book.Entry
	// reaching is which of them a connection is being held to.
	reaching map[string]bool
	err      error
}

type pathsLoaded struct {
	peer  string
	paths []proto.Served
	err   error
}

type historyLoaded struct {
	peer    string
	log     []convo.Message
	waiting map[string]bool
	err     error
}

// framePainted says the far end's terminal has changed and the view should be redrawn. The screen
// itself is shared, so nothing about it travels in the message.
type framePainted struct{}

type watchEnded struct{ err error }

type saidIt struct{ err error }

func loadPeers(back Backend) tea.Cmd {
	return func() tea.Msg {
		peers, err := back.Peers()
		return peersLoaded{peers: peers, reaching: back.Reaching(), err: err}
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
		if err != nil {
			return historyLoaded{peer: with.Name, err: err}
		}

		// Which of them are still on their way, read at the same moment as the conversation: two
		// reads a frame apart would show a message as both sent and waiting.
		queued, err := back.Waiting(with)
		return historyLoaded{peer: with.Name, log: log, waiting: queued, err: err}
	}
}

// ---------------------------------------------------------------- helpers

func (m Model) peer() (book.Entry, bool) {
	if m.atPeer < 0 || m.atPeer >= len(m.peers) {
		return book.Entry{}, false
	}
	return m.peers[m.atPeer], true
}

// path is the namespace that is open, which is whichever step of the tree was entered.
func (m Model) path() (proto.Served, bool) {
	if m.atPath < 0 || m.atPath >= len(m.steps) {
		return proto.Served{}, false
	}

	at := m.steps[m.atPath]
	if !at.is {
		return proto.Served{}, false
	}
	return at.served, true
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
// say writes a message down. It does not send it: that happens next, and takes as long as somebody
// else's network takes, which is not how long a person should watch an empty screen.
func say(back Backend, to book.Entry, body string) tea.Cmd {
	return func() tea.Msg {
		return saidIt{err: back.Compose(to, body)}
	}
}

// deliver sends what is queued, and says how it went.
func deliver(back Backend, to book.Entry) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()

		return delivered{err: back.Deliver(ctx, to)}
	}
}

type delivered struct{ err error }

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

// The room a live screen has, inside the panel it is drawn in: the two borders and the padding
// either side. Two columns too many and every line of a terminal wraps.
func (m Model) viewWidth() int {
	got := m.width - 4
	if got < 20 {
		return 20
	}
	return got
}

func (m Model) viewHeight() int {
	got := m.height - 5
	if got < 4 {
		return 4
	}
	return got
}

// Identity is this device, as the header shows it.
// Reach says how this device is reachable while the interface is open.
type Reach byte

const (
	// ReachServing: this process holds the address, so it is the node.
	ReachServing Reach = iota
	// ReachDaemon: another process on this machine holds it and is answering for this identity.
	ReachDaemon
)

type Identity struct {
	Name string
	ID   string
	// User is the person this machine belongs to, written the way authorized_keys writes a key.
	// It is what tells your own machines apart from everybody else's in the list.
	User string
	// How this device is reachable while the interface is open.
	Reach Reach
}

// loadMine reads what this device serves, from its own config.
func loadMine(back Backend) tea.Cmd {
	return func() tea.Msg {
		mine, err := back.Mine()
		return pathsLoaded{peer: "", paths: mine, err: err}
	}
}

// idOf turns a printed identity back into one, for the rows that show this device.
func idOf(text string) node.ID {
	id, err := node.ParseID(text)
	if err != nil {
		return node.ID{}
	}
	return id
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

// arrived says something came in from another device.
type arrived struct{}

// waitForArrival blocks until the far end says something, so an open conversation shows a message
// as it lands rather than when a key is next pressed.
//
// One at a time, re-armed after each: a channel read is what a Bubble Tea command is for, and
// polling on a timer would either be late or wake a laptop for nothing.
func listenFor(from <-chan struct{}) tea.Cmd {
	if from == nil {
		return nil
	}
	return func() tea.Msg {
		if _, ok := <-from; !ok {
			return nil
		}
		return arrived{}
	}
}

// Held is one thing in a files namespace of this machine's own.
type Held struct {
	Name string
	Size int64
	// At is when it was last written, for a list that reads newest first.
	At time.Time
}

// heldLoaded carries a directory listing back.
type heldLoaded struct {
	path string
	held []Held
	err  error
}

// loadHeld reads what is in one of this machine's own files namespaces.
func loadHeld(back Backend, path string) tea.Cmd {
	return func() tea.Msg {
		held, err := back.Holding(path)
		return heldLoaded{path: path, held: held, err: err}
	}
}
