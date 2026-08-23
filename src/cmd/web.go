package cmd

import (
	"context"
	"crypto/hmac"
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
	var (
		addr    string
		cert    string
		keyFile string
	)

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Open drop in a browser on this machine",
		Long: "web runs a small server and serves a page that talks to it. A browser cannot dial\n" +
			"another device directly, so this node does it on the page's behalf.\n\n" +
			"By default it binds 127.0.0.1 and refuses anything that did not come from this machine:\n" +
			"the page acts as this node, so reaching it is the same as sitting at the keyboard.\n\n" +
			"Give --addr an interface address and it answers the network instead, which is what a\n" +
			"phone needs. There is no password on the page, so only do that on a network you trust.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWeb(cmd.Context(), addr, cert, keyFile)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7777", "where to listen; a non-loopback address opens it to the network")
	cmd.Flags().StringVar(&cert, "tls-cert", "", "a certificate, so a phone can install the page")
	cmd.Flags().StringVar(&keyFile, "tls-key", "", "the key for --tls-cert")

	return cmd
}

func runWeb(parent context.Context, addr, cert, keyFile string) error {
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

	// Binding anywhere but loopback is the opt-in: nobody types an interface address by accident,
	// and the guard would otherwise refuse every request to the address they asked for.
	if remote := !loopbackAddr(addr); remote {
		site.AllowRemote()
		fmt.Fprintf(os.Stderr, "drop: %s is reachable from the network, and the page acts as this\n"+
			"     device with nothing asked of whoever opens it: conversations, sending, terminals.\n"+
			"     Use 127.0.0.1 and an ssh tunnel if that is not what you want.\n\n", addr)
	}

	// The node serves while the page is open, so a peer can reach this device and what it sends
	// appears without a reload.
	go serveLoop(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.Handle(s, from, proto.Policy{
				Mounts: cfg.Mounts,
				Allow:  accepting(pinned, false),
				Who:    whoIs(pinned),
				Message: receiving(pinned, cfg.OpenLinks, func(from node.ID, m convo.Message) {
					site.Arrived(m)
				}),
			})
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.AnswerHello(s, greeting(pinned, cfg.Mounts, from))
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
	scheme := "http"
	if cert != "" {
		scheme = "https"
	}
	fmt.Printf("  %s://%s\n\n", scheme, listener.Addr())
	fmt.Println("ctrl-c to stop")

	// A phone will only install a page served over TLS, and installing is what puts drop in the
	// share sheet. `tailscale cert` issues one for a tailnet name, which is the least painful way
	// to have a real certificate on a machine with no public address.
	if cert != "" || keyFile != "" {
		if cert == "" || keyFile == "" {
			return fmt.Errorf("--tls-cert and --tls-key go together")
		}
		if err := server.ServeTLS(listener, cert, keyFile); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}

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

func (b *bridge) SendFile(ctx context.Context, to book.Entry, path, name string, size int64, body io.Reader) error {
	conn, s, err := reach(ctx, b.node, b.lan, to, node.ALPNSession)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer s.Close()

	// A known size, so the far end can resume it the same as any other file. The reader is the
	// upload itself rather than a path: nothing is written to this machine on the way through.
	source := proto.Source{Name: name, Size: size, Mode: 0o644, Reader: body}

	if err := proto.SendFiles(ctx, s, path, []proto.Source{source}, node.DisplayName(), nil); err != nil {
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

// loopbackAddr reports whether a listen address only this machine can reach.
//
// An empty host means every interface, which is the least local thing there is.
func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Spaces asks a peer what it serves.
func (b *bridge) Spaces(ctx context.Context, to book.Entry) ([]web.Space, error) {
	conn, s, err := reach(ctx, b.node, b.lan, to, node.ALPNHello)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	defer s.Close()

	hello, err := proto.AskHello(s)
	if err != nil {
		return nil, err
	}

	out := make([]web.Space, 0, len(hello.Serves))
	for _, served := range hello.Serves {
		out = append(out, web.Space{
			Path:     served.Path,
			Kind:     served.Kind.String(),
			Writable: served.Writable,
		})
	}
	return out, nil
}

// Self is who this node is, for the page to show.
func (b *bridge) Self(ctx context.Context) (web.Identity, error) {
	who := web.Identity{Name: node.DisplayName(), ID: b.node.ID().String()}

	for _, at := range discovery.LocalAddrs(b.node) {
		who.Addrs = append(who.Addrs, at.String())
	}
	return who, nil
}

// Offer puts this device up for pairing and reports the name it paired with.
//
// The same act as `drop pair`, reachable from the page — because a device with nothing paired is a
// dead end, and reaching for a terminal to fix that is not an interface.
func (b *bridge) Offer(ctx context.Context) (string, <-chan string, error) {
	code, err := proto.NewCode()
	if err != nil {
		return "", nil, err
	}

	pinned, err := book.Load()
	if err != nil {
		return "", nil, err
	}

	invite := ticketFor(b.node.ID(), code, discovery.LocalAddrs(b.node))
	done := make(chan string, 1)

	go serveLoop(ctx, b.node, map[string]func(node.ID, *iroh.Stream){
		node.ALPNPair: func(from node.ID, s *iroh.Stream) {
			defer s.Close()

			p, err := proto.AnswerPairing(s, b.node.ID(), node.DisplayName(), written(discovery.LocalAddrs(b.node)))
			if err != nil {
				return
			}
			// The far end has to prove it was given the code, not merely the address.
			if !hmac.Equal(p.Proof, codeProof(code, from, b.node.ID())) {
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
		},
	})

	return invite, done, nil
}

// Join takes a ticket another device is showing.
func (b *bridge) Join(ctx context.Context, ticket string) (string, error) {
	return joinWith(ctx, b.node, b.lan, ticket, "")
}
