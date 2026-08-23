package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/web"
)

func newWebCmd() *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Open drop in a browser on this machine",
		Long: "web runs a small server on loopback and serves a page that talks to it. A browser\n" +
			"cannot dial another device directly, so this node does it on the page's behalf.\n\n" +
			"It binds 127.0.0.1 and refuses anything that did not come from this machine: the page\n" +
			"acts as this node, so reaching it is the same as sitting at the keyboard.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWeb(cmd.Context(), addr)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7777", "where to listen; loopback only")

	return cmd
}

func runWeb(parent context.Context, addr string) error {
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

	lan, err := discovery.StartLAN(ctx, n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: local discovery unavailable: %v\n", err)
	}

	startRendezvous(ctx, n)

	site := web.New(&bridge{node: n, lan: lan})

	// The node serves while the page is open, so a peer can reach this device and what it sends
	// appears without a reload.
	go serveLoop(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.Handle(s, from, proto.Policy{
				Mounts: cfg.Mounts,
				Allow:  accepting(pinned, false),
				Message: receiving(pinned, cfg.OpenLinks, func(from node.ID, m convo.Message) {
					site.Arrived(m)
				}),
			})
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.AnswerHello(s, proto.Hello{Name: node.DisplayName(), Version: version})
		},
	})

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	server := &http.Server{
		Handler:           site.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	fmt.Printf("%s  %s\n\n", node.DisplayName(), n.ID())
	fmt.Printf("  http://%s\n\n", listener.Addr())
	fmt.Println("ctrl-c to stop")

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// bridge is how the page reaches other devices: it holds the node, so the web layer does not have
// to know what a peer or a transport is.
type bridge struct {
	node *node.Node
	lan  *discovery.LAN
}

func (b *bridge) Say(ctx context.Context, to book.Entry, kind byte, body string) error {
	if _, err := compose(to, kind, body, ""); err != nil {
		return err
	}
	// Queued already, so a device that is asleep is not an error: this only reports whether it
	// went out now.
	if _, err := deliver(ctx, b.node, b.lan, to); err != nil {
		return err
	}
	return nil
}

func (b *bridge) SendFile(ctx context.Context, to book.Entry, name string, size int64, body io.Reader) error {
	conn, s, err := reach(ctx, b.node, b.lan, to, node.ALPNSession)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer s.Close()

	// A known size, so the far end can resume it the same as any other file. The reader is the
	// upload itself rather than a path: nothing is written to this machine on the way through.
	source := proto.Source{Name: name, Size: size, Mode: 0o644, Reader: body}

	if err := proto.SendFiles(ctx, s, "/inbox", []proto.Source{source}, node.DisplayName(), nil); err != nil {
		return err
	}
	noteFile(to.ID, convo.Out, name, size)
	return nil
}

// Watch reads a namespace on another device and writes what arrives to out.
//
// Read-only, deliberately: the page never sends keystrokes back. A browser tab is not a terminal
// this node controls, and a watcher that could type would be a shell handed to whatever loaded the
// page.
func (b *bridge) Watch(ctx context.Context, to book.Entry, path string, into web.Terminal) error {
	conn, s, err := reach(ctx, b.node, b.lan, to, node.ALPNSession)
	if err != nil {
		return err
	}
	defer conn.Close()

	d, err := proto.OpenDuplex(ctx, s, path, path, node.DisplayName())
	if err != nil {
		return err
	}

	// The far end says how big its terminal is, and the screen follows it. Without this the
	// grid stays whatever it started as and every line wraps in the wrong place.
	d.OnResize = func(cols, rows uint16) { into.Resize(int(cols), int(rows)) }

	// Half-closing says there will be no input, which is what lets the far end stop waiting
	// on one. The page never sends keystrokes: a browser tab is not a terminal this node
	// controls, and a watcher that could type would be a shell handed to whoever loaded it.
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
