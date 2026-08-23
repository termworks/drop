package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
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

	lan, _ := discovery.StartLAN(ctx, n)
	startRendezvous(ctx, n)

	program := tea.NewProgram(
		tui.New(&live{node: n, lan: lan}),
		tea.WithAltScreen(),
		tea.WithContext(ctx),
	)

	_, err = program.Run()
	return err
}

// live is the interface's view of a running node.
type live struct {
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
