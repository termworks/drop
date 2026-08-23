package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// flushEvery is how often a chat retries whatever is still queued.
const flushEvery = 15 * time.Second

func newChatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chat <name|id>",
		Short: "Talk to a paired device",
		Long: "Lines you type are sent; what arrives is printed. Anything that cannot be delivered\n" +
			"stays queued and goes out when the far end appears.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd.Context(), args[0])
		},
	}
}

func runChat(parent context.Context, target string) error {
	entry, err := book.Resolve(target)
	if err != nil {
		return err
	}

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

	store, err := convo.Open(entry.ID)
	if err != nil {
		return err
	}
	history, err := store.History()
	if err != nil {
		return err
	}
	for _, m := range history[max(0, len(history)-20):] {
		fmt.Println(render(entry.Name, m))
	}

	lan, err := discovery.StartLAN(ctx, n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: mDNS unavailable: %v\n", err)
	}

	// Listening while typing: the far end can start a session at any moment, and a chat that only
	// receives when it is sending is not a chat.
	policy := proto.Policy{
		Mounts: chatMounts(),
		Allow:  accepting(pinned, false),
		Message: receiving(pinned, false, func(from node.ID, m convo.Message) {
			fmt.Printf("\r%s\n", render(nameFor(pinned, from), m))
		}),
	}
	go serveLoop(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.Handle(s, from, policy)
		},
	})

	fmt.Printf("\ntalking to %s; ctrl-c or ctrl-d to stop\n\n", entry.Name)

	go flushLoop(ctx, n, lan, entry)

	lines := make(chan string)
	go func() {
		defer close(lines)
		scan := bufio.NewScanner(os.Stdin)
		scan.Buffer(make([]byte, 0, 64<<10), convo.MaxBody)
		for scan.Scan() {
			lines <- scan.Text()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			text := strings.TrimSpace(line)
			if text == "" {
				continue
			}
			if _, err := compose(entry, convo.KindText, text, ""); err != nil {
				fmt.Fprintf(os.Stderr, "drop: %v\n", err)
				continue
			}
			// Sent in the background so a slow or absent far end does not stop the typing.
			go func() {
				if _, err := deliver(ctx, n, lan, entry); err != nil {
					fmt.Printf("  (queued: %v)\n", err)
				}
			}()
		}
	}
}

// chatMounts is the one namespace a chat serves while it is open.
func chatMounts() *ns.Table {
	table := ns.NewTable()
	_ = table.Add(ns.Mount{Path: "/chat", Kind: ns.KindChat})
	return table
}

// flushLoop keeps trying whatever is still queued, so a device coming back gets the backlog
// without anyone typing again.
func flushLoop(ctx context.Context, n *node.Node, lan *discovery.LAN, entry book.Entry) {
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_, _ = deliver(ctx, n, lan, entry)
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
