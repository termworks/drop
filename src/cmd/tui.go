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

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/tui"
	"io"
)

func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "drop in a full-screen terminal interface",
		Long: "ui shows your devices, what each one shares with you, and whatever is at a path.\n\n" +
			"It is a view onto the same model as everything else: which paths appear was decided\n" +
			"by the far device, not here.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd.Context())
		},
	}
}

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

	// The interface serves while it is open, so a device that pairs with it can reach it — and
	// so what arrives lands in a conversation rather than being refused.
	ears := listenOn(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.Handle(s, from, proto.Policy{
				Mounts:  cfg.Mounts,
				Allow:   accepting(pinned, false),
				Who:     whoIs(pinned),
				Message: receiving(pinned, cfg.OpenLinks, nil),
			})
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.AnswerHello(s, greeting(pinned, cfg.Mounts, from))
		},
	})

	program := tea.NewProgram(
		tui.New(&live{node: n, lan: lan, ears: ears}),
		tea.WithAltScreen(),
		tea.WithContext(ctx),
	)

	_, err = program.Run()
	return err
}

// live is the interface's view of a running node.
type live struct {
	ears *listener
	node *node.Node
	lan  *discovery.LAN
}

func (l *live) Peers() ([]book.Entry, error) {
	pinned, err := book.Load()
	if err != nil {
		return nil, err
	}
	return pinned.All(), nil
}

func (l *live) Serves(ctx context.Context, with book.Entry) ([]proto.Served, error) {
	conn, s, err := reach(ctx, l.node, l.lan, with, node.ALPNHello)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
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

func (l *live) Say(ctx context.Context, to book.Entry, body string) error {
	if _, err := compose(to, convo.KindText, body, ""); err != nil {
		return err
	}
	_, err := deliver(ctx, l.node, l.lan, to)
	return err
}

// Watch reads a live path into a screen, nudging the interface whenever the picture changes.
func (l *live) Watch(ctx context.Context, on book.Entry, path string, into io.Writer, resize func(cols, rows int)) error {
	conn, s, err := reach(ctx, l.node, l.lan, on, node.ALPNSession)
	if err != nil {
		return err
	}
	defer conn.Close()

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
		conn.Close()
		return ctx.Err()
	}
}

var _ = fmt.Sprintf

func (l *live) Self() (tui.Identity, error) {
	id := l.node.ID().String()
	if len(id) > 12 {
		id = id[:12] + "…"
	}
	return tui.Identity{Name: node.DisplayName(), ID: id}, nil
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

	invite := ticketFor(l.node.ID(), code, discovery.LocalAddrs(l.node))
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
