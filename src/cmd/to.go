package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

func newToCmd() *cobra.Command {
	var (
		as   string
		wait time.Duration
	)

	cmd := &cobra.Command{
		Use:   "to <peer>[/path] [argument...]",
		Short: "Open a namespace on another device",
		Long: "What happens is decided by what is mounted there, not by a flag here.\n\n" +
			"  drop to laptop/inbox report.pdf     a files namespace takes files\n" +
			"  drop to laptop/inbox -              and - is standard input\n" +
			"  drop to laptop/chat \"on my way\"      a chat namespace takes words\n" +
			"  drop to laptop/open https://…       a link namespace takes a URL\n" +
			"  drop to laptop/logs                 a stream namespace is read\n" +
			"  drop to laptop/term                 a tty namespace is watched",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := ns.ParseAddress(args[0])
			if err != nil {
				return err
			}
			return openNamespace(cmd.Context(), addr, args[1:], as, wait)
		},
	}

	cmd.Flags().StringVar(&as, "as", "stdin", "the name to give standard input")
	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the device")

	return cmd
}

// openNamespace works out what the arguments mean, opens the namespace, and lets the far end
// refuse if it is not that kind of thing.
func openNamespace(parent context.Context, addr ns.Address, args []string, stdinName string, wait time.Duration) error {
	entry, err := book.Resolve(addr.Peer)
	if err != nil {
		return err
	}

	switch guess(args) {
	case ns.KindFiles:
		sources, err := gather(args, stdinName)
		if err != nil {
			return err
		}
		return sendTo(parent, entry, addr.Path, sources, wait)

	case ns.KindLink:
		return sendMessageTo(parent, entry, addr.Path, convo.KindLink, args[0], wait)

	case ns.KindChat:
		return sendMessageTo(parent, entry, addr.Path, convo.KindText, strings.Join(args, " "), wait)

	default:
		return readFrom(parent, entry, addr, wait)
	}
}

// guess reads the arguments rather than asking for a mode: a path that exists is a file, a URL is
// a link, other words are a message, and nothing at all means "show me what is there".
func guess(args []string) ns.Kind {
	if len(args) == 0 {
		return ns.KindStream
	}
	if len(args) == 1 && (strings.HasPrefix(args[0], "http://") || strings.HasPrefix(args[0], "https://")) {
		return ns.KindLink
	}
	for _, arg := range args {
		if arg == "-" {
			return ns.KindFiles
		}
		if _, err := os.Stat(arg); err != nil {
			return ns.KindChat
		}
	}
	return ns.KindFiles
}

func sendTo(parent context.Context, entry book.Entry, path string, sources []proto.Source, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, wait)
	defer cancel()

	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	lan, _ := discovery.StartLAN(ctx, n)

	conn, s, err := reach(ctx, n, lan, entry, node.ALPNSession)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer s.Close()

	bar := &progress{}
	defer bar.clear()

	if err := proto.SendFiles(ctx, s, path, sources, node.DisplayName(), bar.update); err != nil {
		return err
	}
	for _, src := range sources {
		noteFile(entry.ID, convo.Out, src.Name, src.Size)
	}
	fmt.Printf("\nsent %d item(s) to %s%s\n", len(sources), entry.Name, path)
	return nil
}

func sendMessageTo(parent context.Context, entry book.Entry, path string, kind byte, body string, wait time.Duration) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("nothing to send")
	}

	m, err := compose(entry, kind, body, "")
	if err != nil {
		return err
	}
	fmt.Println(render(entry.Name, m))

	ctx, cancel := context.WithTimeout(parent, wait)
	defer cancel()

	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	lan, _ := discovery.StartLAN(ctx, n)

	sent, err := deliverTo(ctx, n, lan, entry, path)
	if err != nil {
		// Queued is not lost. A device that is off is the normal case, not a failure to report as
		// one, so this says where the message is rather than only what went wrong.
		fmt.Printf("queued for %s: %v\n", entry.Name, err)
		return nil
	}
	if sent > 0 {
		fmt.Println("delivered")
	}
	return nil
}

// readFrom opens a live namespace and writes what arrives to this terminal.
func readFrom(parent context.Context, entry book.Entry, addr ns.Address, wait time.Duration) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	find, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	lan, _ := discovery.StartLAN(ctx, n)

	conn, s, err := reach(find, n, lan, entry, node.ALPNSession)
	if err != nil {
		return err
	}
	defer conn.Close()

	d, err := proto.OpenDuplex(ctx, s, addr.Path, addr.Path, node.DisplayName())
	if err != nil {
		return err
	}

	// Raw mode and a size only make sense for a terminal, but what is typed goes over either way:
	// a pipe is how a script drives a shell on another machine, and refusing its input made this
	// usable only by hand.
	local := int(os.Stdin.Fd())
	if term.IsTerminal(local) {
		state, err := term.MakeRaw(local)
		if err == nil {
			defer term.Restore(local, state)
		}
		if w, h, err := term.GetSize(local); err == nil {
			_ = d.Resize(uint16(w), uint16(h))
		}
	}

	// Closed when standard input runs out, so a piped-in script ends the far side's shell rather
	// than leaving it waiting for a line that is never coming.
	go func() {
		_, _ = io.Copy(d, os.Stdin)
		_ = d.Close()
	}()

	fmt.Fprintf(os.Stderr, "drop: reading %s%s; ctrl-c to stop\r\n", entry.Name, addr.Path)

	done := make(chan error, 1)
	go func() { done <- d.Pump(os.Stdout) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-done:
		if streamOver(err) {
			return nil
		}
		return err
	}
}
