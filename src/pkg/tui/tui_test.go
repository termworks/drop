package tui

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	t.Helper()

	model := tea.Model(m)
	var queue []tea.Cmd

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

			var got tea.Cmd
			model, got = model.Update(msg)
			queue = append(queue, got)
		}
	}
	return model.(Model)
}

func TestItOpensTheFirstDeviceAndPath(t *testing.T) {
	m := start(t, withOne())

	if len(m.peers) != 1 || m.peers[0].Name != "beta" {
		t.Fatalf("peers = %+v", m.peers)
	}
	if len(m.paths) != 2 {
		t.Fatalf("paths = %+v", m.paths)
	}
	if at, _ := m.path(); at.Path != "/friends/chat" {
		t.Fatalf("opened %q, want the first path", at.Path)
	}
}

// The view is what a person sees, so it has to name what is there.
func TestTheViewNamesWhatIsThere(t *testing.T) {
	m := start(t, withOne())

	drawn := m.View()
	for _, want := range []string{"beta", "/friends/chat", "/term", "devices", "shared with you"} {
		if !strings.Contains(drawn, want) {
			t.Fatalf("the view never mentions %q", want)
		}
	}
}

func TestMovingDownOpensTheNextPath(t *testing.T) {
	m := start(t, withOne())

	m.focus = panePaths
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyDown})

	if at, _ := m.path(); at.Path != "/term" {
		t.Fatalf("moved to %q", at.Path)
	}
	if !m.live {
		t.Fatal("opening a tty path did not start a watch")
	}
}

// Moving off a terminal has to end the watch, or it is left being read by nobody.
func TestMovingAwayStopsTheWatch(t *testing.T) {
	back := withOne()
	m := start(t, back)

	m.focus = panePaths
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if !m.live {
		t.Fatal("the watch never started")
	}

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.live {
		t.Fatal("the watch outlived the path it was on")
	}
	if m.screen != nil {
		t.Fatal("the screen was kept after moving away")
	}
}

func TestWritingAMessageSendsIt(t *testing.T) {
	back := withOne()
	m := start(t, back)

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

// Escape must throw the draft away rather than send it.
func TestEscapeAbandonsAMessage(t *testing.T) {
	back := withOne()
	m := start(t, back)

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.writing || m.typing != "" {
		t.Fatalf("still writing %q", m.typing)
	}

	back.mu.Lock()
	defer back.mu.Unlock()
	if len(back.said) != 0 {
		t.Fatalf("an abandoned draft was sent: %+v", back.said)
	}
}

// While composing, q is a letter and not a way out.
func TestQIsALetterWhileWriting(t *testing.T) {
	m := start(t, withOne())

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	if m.typing != "q" {
		t.Fatalf("typing = %q, so q was taken as quit", m.typing)
	}
}

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
	return settle(t, m, m.Init()(), tea.WindowSizeMsg{Width: 120, Height: 30})
}
