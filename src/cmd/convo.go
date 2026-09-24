package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/arch/chat"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/plain"
	"github.com/bresilla/drop/src/pkg/proto"
)

// ChatPath is where a conversation is served, and where a queue for a peer is delivered.
const ChatPath = "/chat"

// deliverTo sends whatever is waiting for a peer and clears what the far end confirms it stored.
//
// Undelivered messages stay queued rather than being dropped, which is what makes sending to a
// device that is asleep work: it goes out when the device comes back.
func deliverTo(ctx context.Context, n *node.Node, lan *discovery.LAN, entry book.Entry, path, archetype string) (int, error) {
	return deliverOver(ctx, best(n, lan), entry, path, archetype)
}

// deliverOver sends what is queued over whatever connection the caller has.
//
// A held one where there is one. Finding a device and standing up a relay session is five seconds;
// a message on a connection that already exists is eight milliseconds. Dialling again for every
// line somebody types throws that away.
func deliverOver(ctx context.Context, over reaches, entry book.Entry, path, archetype string) (int, error) {
	store, err := convo.Open(entry.ID)
	if err != nil {
		return 0, err
	}

	waiting, err := store.Pending()
	if err != nil {
		return 0, err
	}
	if len(waiting) == 0 {
		return 0, nil
	}

	done, s, err := over.To(ctx, entry, node.ALPNSession)
	if err != nil {
		return 0, err
	}
	defer func() { _ = done.Close() }()
	defer func() { _ = s.Close() }()
	defer stopStreamOnDone(ctx, s)()

	conn, err := proto.Open(s, path, archetype, 0, "", node.DisplayName())
	if err == nil {
		var stored []string
		if stored, err = chat.Send(conn, waiting); err == nil {
			return len(stored), store.Delivered(stored...)
		}
	}

	// A settled refusal is an answer. Leaving those queued would retry them against a decision on
	// every connection from now on, and go on telling the sender they are on their way. A device
	// answering with one namespace up — a chat, a cast, a handoff — says no to everything else it
	// will serve again a minute later, and that is not settled.
	if proto.Settled(err) {
		if done := ids(waiting); len(done) > 0 {
			if clearErr := store.Delivered(done...); clearErr != nil {
				return 0, errors.Join(err, fmt.Errorf("clearing messages after the refusal: %w", clearErr))
			}
		}
	}
	return 0, err
}

// compose queues a message for a peer without needing the network.
func compose(entry book.Entry, kind byte, body, extra string) (convo.Message, error) {
	store, err := convo.Open(entry.ID)
	if err != nil {
		return convo.Message{}, err
	}

	m, err := convo.New(kind, body, extra)
	if err != nil {
		return convo.Message{}, err
	}
	return m, store.Queue(m)
}

// receiving stores an arriving message and acts on the kinds that ask for it.
func receiving(pinned *book.Book, opener string, show func(node.ID, convo.Message)) func(node.ID, convo.Message) error {
	return func(from node.ID, m convo.Message) error {
		store, err := convo.Open(from)
		if err != nil {
			return err
		}

		fresh, err := store.Add(m)
		if err != nil {
			return err
		}
		// A resend of something already stored is acknowledged again but not acted on twice.
		if !fresh {
			return nil
		}

		if show != nil {
			show(from, m)
		}
		if m.Kind == convo.KindLink && opener != "" {
			openWith(opener, m.Body, browserOpeners)
		}
		return nil
	}
}

// defaultOpener is what opens a link when nothing says what should: $DROP_OPENER, or the desktop's own.
func defaultOpener() string {
	if opener := os.Getenv("DROP_OPENER"); opener != "" {
		return opener
	}
	return "xdg-open"
}

func openWithBrowser(link string, gate *browserGate) bool {
	return openWith(defaultOpener(), link, gate)
}

// openWith hands a link to a command: the words of the command, then the link. Detached, because
// drop is not the thing that should die if a browser does.
func openWith(opener, link string, gate *browserGate) bool {
	words := strings.Fields(opener)
	if len(words) == 0 {
		return false
	}
	if len(link) > maxOpenedLink || (!strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://")) {
		return false
	}
	if !gate.take(time.Now()) {
		return false
	}

	cmd := exec.Command(words[0], append(words[1:], link)...)
	if err := cmd.Start(); err != nil {
		gate.give()
		fmt.Fprintf(os.Stderr, "drop: could not open %s: %v\n", plain.Text(link, MaxSaid), err)
		return false
	}
	go func() {
		defer gate.give()
		_ = cmd.Wait()
	}()
	return true
}

const (
	maxOpenedLink       = 8 << 10
	maxBrowserProcesses = 4
	maxBrowserStarts    = 8
	browserWindow       = time.Minute
)

type browserGate struct {
	processes chan struct{}
	mu        sync.Mutex
	started   []time.Time
}

func newBrowserGate() *browserGate {
	return &browserGate{processes: make(chan struct{}, maxBrowserProcesses)}
}

func (g *browserGate) take(now time.Time) bool {
	select {
	case g.processes <- struct{}{}:
	default:
		return false
	}

	g.mu.Lock()
	cutoff := now.Add(-browserWindow)
	kept := g.started[:0]
	for _, at := range g.started {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	g.started = kept
	if len(g.started) >= maxBrowserStarts {
		g.mu.Unlock()
		<-g.processes
		return false
	}
	g.started = append(g.started, now)
	g.mu.Unlock()
	return true
}

func (g *browserGate) give() { <-g.processes }

var browserOpeners = newBrowserGate()

// nameFor is what to call a peer in a listing.
func nameFor(pinned *book.Book, id node.ID) string {
	if entry, ok := pinned.ByID(id); ok {
		return entry.Name
	}
	return node.Brief(id)
}

// render prints one message the way a log reads.
// render is one message as a line of a conversation.
//
// What somebody said is theirs, and it is also bytes going to a terminal. Kept as it arrived on the
// disk, because a conversation is a record of what was said; made safe here, because this is where
// it stops being a record and starts being output.
func render(who string, m convo.Message) string {
	arrow := "→"
	if m.Dir == convo.In {
		arrow = "←"
	}

	stamp := m.When().Format("15:04")
	body, extra := plain.Text(m.Body, MaxSaid), plain.Line(m.Extra)
	switch m.Kind {
	case convo.KindLink:
		return fmt.Sprintf("%s %s %-12s link  %s", stamp, arrow, who, body)
	case convo.KindFile:
		return fmt.Sprintf("%s %s %-12s file  %s (%s)", stamp, arrow, who, body, extra)
	case convo.KindEvent:
		return fmt.Sprintf("%s   %-12s %s", stamp, who, body)
	default:
		return fmt.Sprintf("%s %s %-12s %s", stamp, arrow, who, body)
	}
}

// MaxSaid is how much of one message a line of a conversation shows. Generous, because a message is
// the thing somebody wanted to say, and bounded, because it is going onto somebody else's screen.
const MaxSaid = 2000

// noteFile records a file changing hands, so `drop me log` reads as the whole story.
func noteFile(with node.ID, dir byte, name string, size int64) error {
	store, err := convo.Open(with)
	if err != nil {
		return fmt.Errorf("recording %s in the conversation: %w", name, err)
	}
	if err := store.Note(convo.KindFile, dir, name, bytes(size)); err != nil {
		return fmt.Errorf("recording %s in the conversation: %w", name, err)
	}
	return nil
}

// kindName is what a config sees a message kind as.
func kindName(kind byte) string {
	switch kind {
	case convo.KindLink:
		return "link"
	case convo.KindFile:
		return "file"
	case convo.KindEvent:
		return "event"
	default:
		return "text"
	}
}

// ids is what a batch of messages is called, for taking them off the queue.
func ids(all []convo.Message) []string {
	out := make([]string, 0, len(all))
	for _, m := range all {
		out = append(out, m.ID)
	}
	return out
}
