package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	mu        sync.Mutex
	peers     []book.Entry
	serves    map[string][]proto.Served
	log       []convo.Message
	said      []string
	watched   string
	offered   bool
	took      string
	sentFiles []string
	posted    []string
	refuse    error
	paired    chan string
	stream    string
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
				{Path: "/inbox", Kind: ns.KindFiles},
				{Path: "/open", Kind: ns.KindLink},
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
	if len(m.paths) != len(withOne().serves["beta"]) {
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
	m := openPath(t, withOne(), "/term")

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

func (f *fake) Offer(ctx context.Context) (string, <-chan string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.offered = true
	done := make(chan string, 1)
	f.paired = done

	return "7b9773d9#code#192.168.1.1:47777", done, nil
}

func (f *fake) Send(ctx context.Context, to book.Entry, path string, files []string, progress func(string, int64, int64)) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.refuse != nil {
		return f.refuse
	}
	// Halfway, then all the way: a progress bar that only ever reports the end is one nobody can
	// tell from a bar that is broken.
	progress(files[0], 1, 2)
	progress(files[0], 2, 2)

	f.sentFiles = append(f.sentFiles, path+" "+files[0])
	return nil
}

func (f *fake) Post(ctx context.Context, to book.Entry, path string, kind byte, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.refuse != nil {
		return f.refuse
	}
	f.posted = append(f.posted, path+" "+body)
	return nil
}

func (f *fake) Join(ctx context.Context, ticket string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if ticket == "" {
		return "", errors.New("that does not look like a ticket")
	}
	f.took = ticket
	return "tron", nil
}

// A device with nothing paired must not be a dead end: the way out has to be on the screen.
func TestPairingCanBeStartedFromTheInterface(t *testing.T) {
	back := &fake{}
	m := start(t, back)

	if len(m.peers) != 0 {
		t.Fatal("this case is about having nothing paired")
	}
	if !strings.Contains(m.View(), "p") {
		t.Fatal("the empty screen does not offer a way to pair")
	}

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})

	if m.linking == nil {
		t.Fatal("p did not start a pairing")
	}
	if !strings.Contains(m.View(), "drop pair") {
		t.Fatalf("the pairing screen does not show the ticket:\n%s", m.View())
	}
}

// Finishing has to bring the device into the list without anyone reaching for a second terminal.
func TestPairingFinishingReloadsTheDevices(t *testing.T) {
	back := &fake{}
	m := start(t, back)

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if m.linking == nil {
		t.Fatal("no pairing to finish")
	}

	back.peers = []book.Entry{{Name: "beta", ID: idFor(2), Secret: make([]byte, book.SecretBytes)}}
	m = settle(t, m, pairDone{with: "beta"})

	if m.linking != nil {
		t.Fatal("the pairing screen stayed up after it finished")
	}
	if len(m.peers) != 1 || m.peers[0].Name != "beta" {
		t.Fatalf("the new device did not appear: %+v", m.peers)
	}
}

// Escape has to get out of it, or a mistaken keystroke traps you on that screen.
func TestPairingCanBeAbandoned(t *testing.T) {
	m := start(t, &fake{})

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.linking != nil {
		t.Fatal("escape did not leave the pairing screen")
	}
}

// Pairing has two sides, and a device that can only show a code can only ever be found. Typing in
// a ticket has to reach the backend, or a phone showing a code has nobody to answer it.
func TestATicketCanBeTypedIn(t *testing.T) {
	back := &fake{}
	m := settle(t, start(t, back), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})

	if !m.joining {
		t.Fatalf("t did not open the ticket field:\n%s", m.View())
	}

	ticket := "7b9773d9#code"
	for _, r := range ticket {
		m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !strings.Contains(m.View(), ticket) {
		t.Fatalf("what was typed is not on the screen:\n%s", m.View())
	}

	// A mistyped character has to be correctable, or a hundred-character ticket is unusable.
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.joining {
		t.Fatal("the field stayed up after the ticket was entered")
	}

	back.mu.Lock()
	defer back.mu.Unlock()
	if back.took != ticket {
		t.Fatalf("the backend was given %q, not the ticket that was typed", back.took)
	}
}

// Escape has to leave, or the field is a trap: it owns the keyboard while it is up.
func TestTheTicketFieldCanBeLeft(t *testing.T) {
	m := settle(t, start(t, &fake{}), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})

	if m = settle(t, m, tea.KeyMsg{Type: tea.KeyEsc}); m.joining {
		t.Fatal("escape did not leave the ticket field")
	}
}

// Sending a file has to reach the backend with the path the far device named and the file this one
// resolved, or the interface is a form that goes nowhere.
func TestAFileIsSentFromTheInterface(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	back := withOne()
	m := openPath(t, back, "/inbox")

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.putting {
		t.Fatalf("s did not open the send line:\n%s", m.View())
	}

	for _, r := range file {
		m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	back.mu.Lock()
	defer back.mu.Unlock()
	if len(back.sentFiles) != 1 || back.sentFiles[0] != "/inbox "+file {
		t.Fatalf("the backend was asked to send %v", back.sentFiles)
	}
}

// A file that is not there must be said so before anything is dialled: the far end should not be
// woken up to be told nothing is coming.
func TestSendingAMissingFileSaysSo(t *testing.T) {
	back := withOne()
	m := openPath(t, back, "/inbox")

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	for _, r := range "/no/such/file" {
		m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(m.View(), "no such file") {
		t.Fatalf("a missing file was not reported:\n%s", m.View())
	}

	back.mu.Lock()
	defer back.mu.Unlock()
	if len(back.sentFiles) != 0 {
		t.Fatalf("a missing file was still sent: %v", back.sentFiles)
	}
}

// A link path takes a URL the same way, and must not try to complete it against this filesystem.
func TestALinkIsSentFromTheInterface(t *testing.T) {
	back := withOne()
	m := openPath(t, back, "/open")

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	for _, r := range "https://example.com" {
		m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyTab}) // must do nothing here
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	back.mu.Lock()
	defer back.mu.Unlock()
	if len(back.posted) != 1 || back.posted[0] != "/open https://example.com" {
		t.Fatalf("the backend was asked to post %v", back.posted)
	}
}

// Tab completes a path the way a shell does, so a long path is not typed out by hand.
func TestTabCompletesAPath(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"report-one.txt", "report-two.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	finished, options := complete(filepath.Join(dir, "rep"))
	if finished != filepath.Join(dir, "report-") {
		t.Errorf("completed to %q, want the shared prefix", finished)
	}
	if len(options) != 2 {
		t.Errorf("offered %v, want both files", options)
	}
}

// openPath walks the interface to a path on the one paired device, the way a person would.
func openPath(t *testing.T, back *fake, path string) Model {
	t.Helper()

	m := settle(t, start(t, back), tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 6; i++ {
		if at, ok := m.path(); ok && at.Path == path {
			return m
		}
		if m.at != levelPaths {
			t.Fatalf("did not reach the path list, at %v", m.at)
		}
		m = settle(t, m, tea.KeyMsg{Type: tea.KeyDown})
		m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if m.at == levelOpen {
			if at, ok := m.path(); ok && at.Path == path {
				return m
			}
			m = settle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		}
	}
	t.Fatalf("never opened %s", path)
	return m
}
