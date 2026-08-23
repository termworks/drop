package tui

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

func idFor(seed byte) node.ID {
	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

// fake stands in for a node, so the panes can be driven without a network.
type fake struct {
	mu      sync.Mutex
	peers   []book.Entry
	serves  map[string][]proto.Served
	log     []convo.Message
	said    []string
	watched string
	stream  string
}

func (f *fake) Peers() ([]book.Entry, error) { return f.peers, nil }

func (f *fake) Serves(ctx context.Context, with book.Entry) ([]proto.Served, error) {
	return f.serves[with.Name], nil
}

func (f *fake) History(with book.Entry) ([]convo.Message, error) { return f.log, nil }

func (f *fake) Say(ctx context.Context, to book.Entry, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.said = append(f.said, body)
	return nil
}

func (f *fake) Watch(ctx context.Context, on book.Entry, path string, into io.Writer, resize func(int, int)) error {
	f.mu.Lock()
	f.watched = path
	stream := f.stream
	f.mu.Unlock()

	if stream != "" {
		if _, err := io.WriteString(into, stream); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func withOne() *fake {
	return &fake{
		peers: []book.Entry{
			{Name: "beta", ID: idFor(2), Secret: make([]byte, book.SecretBytes)},
		},
		serves: map[string][]proto.Served{
			"beta": {
				{Path: "/friends/chat", Kind: ns.KindChat},
				{Path: "/term", Kind: ns.KindTTY},
			},
		},
	}
}

// settle runs the model until it stops producing work, so a test sees the state a person would.
//
// Commands run with a deadline rather than being waited on: some of them are meant never to
// return — a watch runs until its path is left — and a test that waited would simply hang.
func settle(t *testing.T, m Model, msgs ...tea.Msg) Model {
	return settleFrom(t, m, nil, msgs...)
}

// settleFrom is the same, with a command to run first — which is how startup is driven, since Init
// hands back a batch rather than a message.
func settleFrom(t *testing.T, m Model, seed tea.Cmd, msgs ...tea.Msg) Model {
	t.Helper()

	model := tea.Model(m)
	queue := []tea.Cmd{seed}

	for _, msg := range msgs {
		var cmd tea.Cmd
		model, cmd = model.Update(msg)
		queue = append(queue, cmd)
	}

	for round := 0; round < 20 && len(queue) > 0; round++ {
		next := queue
		queue = nil

		for _, cmd := range next {
			if cmd == nil {
				continue
			}

			arrived := make(chan tea.Msg, 1)
			go func(cmd tea.Cmd) { arrived <- cmd() }(cmd)

			var msg tea.Msg
			select {
			case msg = <-arrived:
			case <-time.After(150 * time.Millisecond):
				continue
			}
			if msg == nil {
				continue
			}

			// A batch is a bag of commands rather than something to hand to Update.
			if batch, ok := msg.(tea.BatchMsg); ok {
				queue = append(queue, batch...)
				continue
			}

			var got tea.Cmd
			model, got = model.Update(msg)
			queue = append(queue, got)
		}
	}
	return model.(Model)
}

// The view is what a person sees, so it has to name what is there.

// Moving off a terminal has to end the watch, or it is left being read by nobody.

// Escape must throw the draft away rather than send it.

// While composing, q is a letter and not a way out.

// The screen is written by the network and read by the interface. This is the shape that would race.
func TestTheScreenSurvivesBeingWrittenWhileRead(t *testing.T) {
	s := newScreen(40, 10)

	// A closed channel rather than time.After: a timer fires once and reaches one receiver, so
	// the other goroutine would wait for a signal that had already been taken.
	done := make(chan struct{})
	time.AfterFunc(200*time.Millisecond, func() { close(done) })

	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_, _ = s.Write([]byte("\x1b[31mbusy\x1b[0m\r\n"))
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = s.Draw()
				s.Resize(40, 10)
			}
		}
	}()
	wg.Wait()
}

// start brings the interface up as a person would find it: peers loaded, the first path open.
func start(t *testing.T, back Backend) Model {
	t.Helper()

	m := New(back)
	return settleFrom(t, m, m.Init(), tea.WindowSizeMsg{Width: 120, Height: 30})
}

func (f *fake) Self() (Identity, error) { return Identity{Name: "alpha", ID: "e88c42df318c…"}, nil }

// A pane is narrow and a path is not. Wrapping would break the column, so the head gives way and
// the tail — which is what tells two paths apart — is kept.

// Every row of a pane has to be the same width, or the borders do not line up.

// A device that never answers must not leave the pane saying "asking…" forever.

// enter is what a person presses to go a level deeper.
func enter(t *testing.T, m Model) Model {
	t.Helper()
	return settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
}

func back(t *testing.T, m Model) Model {
	t.Helper()
	return settle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
}

func TestItStartsOnTheDeviceList(t *testing.T) {
	m := start(t, withOne())

	if m.at != levelDevices {
		t.Fatalf("started at level %d", m.at)
	}
	if len(m.peers) != 1 {
		t.Fatalf("peers = %+v", m.peers)
	}
	// Nothing is asked of a device until you go into it.
	if len(m.paths) != 0 {
		t.Fatalf("paths were fetched before entering: %+v", m.paths)
	}
	if m.me.Name != "alpha" {
		t.Fatalf("this device = %+v", m.me)
	}
}

func TestEnteringADeviceAsksWhatItShares(t *testing.T) {
	m := enter(t, start(t, withOne()))

	if m.at != levelPaths {
		t.Fatalf("at level %d after entering", m.at)
	}
	if len(m.paths) != 2 {
		t.Fatalf("paths = %+v", m.paths)
	}
}

func TestEnteringAPathOpensIt(t *testing.T) {
	m := enter(t, enter(t, start(t, withOne())))

	if m.at != levelOpen {
		t.Fatalf("at level %d", m.at)
	}
	if at, _ := m.path(); at.Path != "/friends/chat" {
		t.Fatalf("opened %q", at.Path)
	}
}

// Going back has to undo the level, not the program.
func TestGoingBackWalksOutOneLevelAtATime(t *testing.T) {
	m := enter(t, enter(t, start(t, withOne())))

	m = back(t, m)
	if m.at != levelPaths {
		t.Fatalf("back from a path left level %d", m.at)
	}

	m = back(t, m)
	if m.at != levelDevices {
		t.Fatalf("back from a device left level %d", m.at)
	}
	if len(m.paths) != 0 {
		t.Fatal("leaving a device kept its paths")
	}
}

// A terminal is watched while it is open and not a moment longer.
func TestAWatchLastsAsLongAsThePathIsOpen(t *testing.T) {
	m := enter(t, start(t, withOne()))

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = enter(t, m)

	if !m.live {
		t.Fatal("entering a tty path did not start a watch")
	}

	m = back(t, m)
	if m.live {
		t.Fatal("the watch outlived the path")
	}
	if m.screen != nil {
		t.Fatal("the screen was kept after leaving")
	}
}

func TestWritingAMessageSendsIt(t *testing.T) {
	back := withOne()
	m := enter(t, enter(t, start(t, back)))

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !m.writing {
		t.Fatal("i did not start a message")
	}

	for _, r := range "hello" {
		m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	back.mu.Lock()
	defer back.mu.Unlock()
	if len(back.said) != 1 || back.said[0] != "hello" {
		t.Fatalf("sent %+v", back.said)
	}
}

// While composing, q is a letter and not a way out.
func TestQIsALetterWhileWriting(t *testing.T) {
	m := enter(t, enter(t, start(t, withOne())))

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	if m.typing != "q" {
		t.Fatalf("typing = %q, so q was taken as quit", m.typing)
	}
}

// q leaves the level rather than the program, until there is nowhere left to go.
func TestQClimbsOutBeforeItQuits(t *testing.T) {
	m := enter(t, enter(t, start(t, withOne())))

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m.at != levelPaths {
		t.Fatalf("q at a path left level %d", m.at)
	}

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m.at != levelDevices {
		t.Fatalf("q at a device left level %d", m.at)
	}
}

// The row is three lines, and every one of them has to be the same width or the block breaks up.
func TestARowIsThreeLinesOfOneWidth(t *testing.T) {
	m := start(t, withOne())
	m.list.SetSize(80, 12)

	drawn := m.list.View()
	rows := strings.Split(drawn, "\n")

	seen := 0
	for _, at := range rows {
		if strings.TrimSpace(at) == "" {
			continue
		}
		seen++
		if got := lipgloss.Width(at); got != 80 {
			t.Fatalf("a row is %d wide, not 80: %q", got, at)
		}
	}
	if seen < rowHeight {
		t.Fatalf("only %d lines drawn for a device", seen)
	}
}

func TestALongValueIsClipped(t *testing.T) {
	got := clip("/one/two/five/eight/nine", 12)

	if lipgloss.Width(got) > 12 {
		t.Fatalf("%q is still %d wide", got, lipgloss.Width(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("%q does not say it was cut", got)
	}
}

func TestAShortValueIsLeftAlone(t *testing.T) {
	if got := clip("/chat", 20); got != "/chat" {
		t.Fatalf("got %q", got)
	}
}

// A device that never answers must not leave the header saying "asking…" forever.
func TestAnUnreachableDeviceIsReported(t *testing.T) {
	m := enter(t, start(t, withOne()))

	m = settle(t, m, pathsLoaded{peer: "beta", err: context.DeadlineExceeded})
	if m.trouble == "" {
		t.Fatal("a failed lookup left nothing to act on")
	}
	if m.loading {
		t.Fatal("still loading after a failure")
	}
}
