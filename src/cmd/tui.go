package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"crypto/hmac"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/tui"
	"io"
)

func runTUI(parent context.Context) error {
	cfg, err := conf.Load()
	if err != nil {
		return err
	}
	cfg.Apply()
	defer cfg.Close()

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	pinned, err := book.Load()
	if err != nil {
		return err
	}

	lan, _ := discovery.StartLAN(ctx, n)
	startRendezvous(ctx, n)

	// Depth one, and a full channel is left alone: the signal carries nothing, so one pending
	// knock means the same as ten, and a device that says a great deal at once still redraws once.
	arriving := make(chan struct{}, 1)

	// One connection per device, kept for as long as the interface is open.
	held := dial.Hold(n, lan, finder(n))
	defer held.Close()

	// The interface serves while it is open, so a device that pairs with it can reach it — and
	// so what arrives lands in a conversation rather than being refused.
	ears := listenOn(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.Handle(s, from, proto.Policy{
				Mounts: cfg.Mounts,
				Allow:  accepting(pinned, false),
				Who:    whoIs(pinned),
				Message: receiving(pinned, cfg.OpenLinks, func(node.ID, convo.Message) {
					knock(arriving)
				}),
				// A file that lands while the interface is open belongs in the conversation the
				// same way it would with the daemon running.
				Done: func(from node.ID, name string, size int64) {
					noteFile(from, convo.In, name, size)
					cfg.FireFile(conf.File{From: nameFor(pinned, from), Name: name, Size: size})
					knock(arriving)
				},
			})
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.AnswerHello(s, greeting(pinned, cfg.Mounts, from))
		},
	})

	program := tea.NewProgram(
		tui.New(&live{node: n, lan: lan, ears: ears, arriving: arriving, held: held}),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)

	_, err = program.Run()
	return err
}

// live is the interface's view of a running node.
type live struct {
	ears     *listener
	node     *node.Node
	lan      *discovery.LAN
	arriving chan struct{}
	// held keeps a connection per device, so a conversation costs one handshake rather than one
	// for every line typed.
	held *dial.Kept
}

// Arrivals is how the interface learns that something landed while it was sitting there.
func (l *live) Arrivals() <-chan struct{} { return l.arriving }

// knock says something happened, without ever waiting for anybody to be listening: a device
// sending a file must not be held up because nothing is on screen to care.
func knock(at chan struct{}) {
	select {
	case at <- struct{}{}:
	default:
	}
}

func (l *live) Peers() ([]book.Entry, error) {
	pinned, err := book.Load()
	if err != nil {
		return nil, err
	}
	return pinned.All(), nil
}

func (l *live) Serves(ctx context.Context, with book.Entry) ([]proto.Served, error) {
	s, err := l.held.To(ctx, with, node.ALPNHello)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	hello, err := proto.AskHello(s)
	if err != nil {
		return nil, err
	}
	return hello.Serves, nil
}

func (l *live) History(with book.Entry) ([]convo.Message, error) {
	store, err := convo.Open(with.ID)
	if err != nil {
		return nil, err
	}
	return store.History()
}

// Compose writes a message into the conversation. Nothing is dialled: it is a disk write, and the
// interface can draw it before anybody has been reached.
func (l *live) Compose(to book.Entry, body string) error {
	_, err := compose(to, convo.KindText, body, "")
	return err
}

// Deliver sends whatever is queued for a device.
func (l *live) Deliver(ctx context.Context, to book.Entry) error {
	_, err := deliver(ctx, l.node, l.lan, to)
	return err
}

// Waiting is which messages are still in the outbox, by id.
func (l *live) Waiting(with book.Entry) (map[string]bool, error) {
	store, err := convo.Open(with.ID)
	if err != nil {
		return nil, err
	}

	queued, err := store.Pending()
	if err != nil {
		return nil, err
	}

	out := make(map[string]bool, len(queued))
	for _, m := range queued {
		out[m.ID] = true
	}
	return out, nil
}

// Send copies files to a path on the far device.
//
// The same call the command line makes, over the interface's own node: a long-lived process should
// not stand up a second endpoint to send a file, and the far end should not see a stranger.
func (l *live) Send(ctx context.Context, to book.Entry, path string, files []string, progress func(string, int64, int64)) error {
	sources, err := gather(files, "")
	if err != nil {
		return err
	}

	s, err := l.held.To(ctx, to, node.ALPNSession)
	if err != nil {
		return err
	}
	defer s.Close()

	if err := proto.SendFiles(ctx, s, path, sources, node.DisplayName(), progress); err != nil {
		return err
	}
	for _, src := range sources {
		noteFile(to.ID, convo.Out, src.Name, src.Size)
	}
	return nil
}

// Post sends one message to a path.
func (l *live) Post(ctx context.Context, to book.Entry, path string, kind byte, body string) error {
	m, err := compose(to, kind, body, "")
	if err != nil {
		return err
	}

	s, err := l.held.To(ctx, to, node.ALPNSession)
	if err != nil {
		return err
	}
	defer s.Close()

	_, err = proto.SendMessages(ctx, s, path, []convo.Message{m}, node.DisplayName())
	return err
}

// Watch reads a live path into a screen, nudging the interface whenever the picture changes.
func (l *live) Watch(ctx context.Context, on book.Entry, path string, into io.Writer, resize func(cols, rows int)) error {
	s, err := l.held.To(ctx, on, node.ALPNSession)
	if err != nil {
		return err
	}

	d, err := proto.OpenDuplex(ctx, s, path, path, node.DisplayName())
	if err != nil {
		return err
	}
	d.OnResize = func(cols, rows uint16) { resize(int(cols), int(rows)) }

	// No keystrokes go back. The interface is a viewer, and a viewer that could type would be a
	// shell handed to whoever walked past the desk.
	_ = d.Close()

	done := make(chan error, 1)
	go func() { done <- d.Pump(into) }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// The stream goes, the connection stays: it is shared with everything else this device is
		// doing, and closing it here would drop a conversation to end a watch.
		s.Close()
		return ctx.Err()
	}
}

var _ = fmt.Sprintf

func (l *live) Self() (tui.Identity, error) {
	// Which process is the node matters to whoever is reading: if the daemon holds the address,
	// this is a view onto something that goes on running after the interface is closed.
	reach := tui.ReachServing
	if !l.node.Own() {
		reach = tui.ReachDaemon
	}

	return tui.Identity{Name: node.DisplayName(), ID: l.node.ID().String(), Reach: reach}, nil
}

// Offer puts this device up for pairing and reports the name it paired with.
//
// The interface shows the ticket and the code while this waits, which is the whole point: a device
// with nothing paired is a dead end, and reaching for a second terminal to fix that is not a design.
func (l *live) Offer(ctx context.Context) (string, <-chan string, error) {
	code, err := proto.NewCode()
	if err != nil {
		return "", nil, err
	}

	invite := ticketFor(l.node.ID(), code)
	done := make(chan string, 1)

	pinned, err := book.Load()
	if err != nil {
		return "", nil, err
	}

	// Registered on the interface's own listener rather than starting a second one. Two accept
	// loops on one endpoint race, and the loser hangs up on a connection it does not know.
	l.ears.Handle(node.ALPNPair, func(from node.ID, s *iroh.Stream) {
		defer s.Close()

		p, err := proto.AnswerPairing(s, l.node.ID(), node.DisplayName(), written(discovery.LocalAddrs(l.node)))
		if err != nil {
			return
		}
		// The far end has to prove it was given the code, not merely the address.
		if !hmac.Equal(p.Proof, codeProof(code, from, l.node.ID())) {
			return
		}

		name := p.Name
		if name == "" {
			name = node.Brief(from)
		}
		pinned.Pair(name, from, p.Secret, p.Addrs...)
		if err := pinned.Save(); err != nil {
			return
		}

		select {
		case done <- name:
		default:
		}
	})

	go func() {
		<-ctx.Done()
		l.ears.Handle(node.ALPNPair, nil)
	}()

	return invite, done, nil
}

// Join takes a ticket another device is showing.
func (l *live) Join(ctx context.Context, ticket string) (string, error) {
	return joinWith(ctx, l.node, l.lan, ticket, "")
}
