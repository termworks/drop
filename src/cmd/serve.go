package cmd

import (
	"context"
	"fmt"
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
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

func newServeCmd() *cobra.Command {
	var quiet bool

	cmd := &cobra.Command{
		Use:     "serve",
		Aliases: []string{"daemon"},
		Short:   "Serve the namespaces this node declares, and stay reachable",
		Long: "What is served comes from the config, not from flags. Every namespace it declares is\n" +
			"available to paired devices at <this node>/<path>.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), quiet)
		},
	}

	cmd.Flags().BoolVar(&quiet, "quiet", false, "only report arrivals and errors")

	return cmd
}

func runServe(parent context.Context, quiet bool) error {
	cfg, err := conf.Load()
	if err != nil {
		return err
	}
	// Settings take effect before the endpoint starts, because the name is read while it comes up.
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

	startRendezvous(ctx, n)

	lan, err := discovery.StartLAN(ctx, n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: mDNS unavailable: %v\n", err)
	}

	go backlog(ctx, n, lan, pinned)

	shells := newTerminals()
	defer shells.stop()

	bar := &progress{}
	policy := proto.Policy{
		Mounts:   cfg.Mounts,
		Allow:    accepting(pinned, false),
		Who:      whoIs(pinned),
		Progress: bar.update,
		Done: func(from node.ID, name string, size int64) {
			fmt.Printf("  received %s (%s)\n", name, bytes(size))
			noteFile(from, convo.In, name, size)
			cfg.FireFile(conf.File{From: nameFor(pinned, from), Name: name, Size: size})
		},
		Message: receiving(pinned, cfg.OpenLinks, func(from node.ID, m convo.Message) {
			fmt.Println(render(nameFor(pinned, from), m))
			cfg.FireMessage(conf.Message{
				From: nameFor(pinned, from), Kind: kindName(m.Kind), Body: m.Body, Path: "/chat",
			})
		}),
		Duplex: serveDuplex(pinned, shells),
	}

	// The address book is re-read before answering anybody, because `drop pair` is a separate
	// process: without this, a device paired while this was running stays a stranger to it.
	go serveLoop(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = pinned.Refresh()
			if err := proto.Handle(s, from, policy); err != nil {
				fmt.Fprintf(os.Stderr, "drop: %v\n", err)
			}
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = pinned.Refresh()
			_ = proto.AnswerHello(s, greeting(pinned, cfg.Mounts, from))
		},
	})

	describe(cfg, n)

	report := time.NewTicker(time.Minute)
	defer report.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nstopping")
			return nil
		case <-report.C:
			if quiet {
				continue
			}
			fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), n.Addr())
		}
	}
}

// describe prints what this node is serving, because a namespace nobody can see is one nobody uses.
func describe(cfg *conf.Config, n *node.Node) {
	fmt.Printf("%s  %s\n", node.DisplayName(), n.ID())
	if cfg.Path != "" {
		fmt.Printf("config %s\n", cfg.Path)
	} else {
		fmt.Printf("no config; serving the defaults\n")
	}

	fmt.Println()
	for _, m := range cfg.Mounts.All() {
		fmt.Printf("  %-24s %-7s %s\n", m.Path, m.Kind, detail(m))
	}
	fmt.Println("\nready; ctrl-c to stop")
}

func detail(m ns.Mount) string {
	switch m.Kind {
	case ns.KindFiles:
		return m.Dir
	case ns.KindStream:
		return m.Command
	case ns.KindTTY:
		if m.Input {
			return "interactive"
		}
		return "read-only"
	case ns.KindLink:
		if m.Action != "" {
			return m.Action
		}
		return "recorded, not opened"
	default:
		return ""
	}
}

// backlog keeps trying whatever is still queued for anybody.
//
// A message to a device that was off is kept rather than lost, and the thing that notices it coming
// back has to be the thing that is always running. Until this, a backlog only moved while somebody
// had `drop chat` open, which is the one moment they do not need it to.
func backlog(ctx context.Context, n *node.Node, lan *discovery.LAN, pinned *book.Book) {
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		// Re-read first: a device paired since this started has a conversation too.
		_ = pinned.Refresh()

		for _, entry := range pinned.Paired() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, _ = deliver(ctx, n, lan, entry)
		}
	}
}
