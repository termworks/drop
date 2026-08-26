package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
)

func newListCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "ls [device[/path]]",
		Short: "What a device shares with you",
		Long: "ls asks a device what it serves, and shows what you may reach.\n\n" +
			"What comes back is filtered by that device: a path shared with someone else is\n" +
			"absent rather than refused, so this is what you have, not what exists.\n\n" +
			"With no argument, it lists what this device serves.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return showOwnTable(reading())
			}
			peer, under, _ := strings.Cut(args[0], "/")
			return listThere(cmd.Context(), peer, "/"+strings.Trim(under, "/"), wait)
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 30*time.Second, "how long to spend finding the device")

	return cmd
}

func listThere(parent context.Context, peer, under string, wait time.Duration) error {
	entry, err := book.Resolve(peer)
	if err != nil {
		return err
	}

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

	done, s, err := best(n, lan).To(find, entry, node.ALPNHello)
	if err != nil {
		return err
	}
	defer done.Close()
	defer s.Close()

	hello, err := proto.AskHello(s)
	if err != nil {
		return err
	}

	shown := make([]proto.Served, 0, len(hello.Serves))
	for _, served := range hello.Serves {
		if under == "/" || covers(under, served.Path) {
			shown = append(shown, served)
		}
	}

	if len(shown) == 0 {
		if under != "/" {
			fmt.Printf("\n%s shares nothing with you under %s\n\n", entry.Name, under)
		} else {
			fmt.Printf("\n%s shares nothing with you\n\n", entry.Name)
		}
		return nil
	}

	paths := make([]string, 0, len(shown))
	column := make([]string, 0, len(shown))
	for _, served := range shown {
		paths = append(paths, served.Path)
		column = append(column, kindOf(served.Archetype))
	}
	width, kind := widest(0, paths), widest(6, column)

	fmt.Printf("\n%s  %s\n\n", entry.Name, node.Brief(entry.ID))
	for _, served := range shown {
		fmt.Printf("  %-*s  %-*s %s\n", width, served.Path, kind, kindOf(served.Archetype), served.About)
	}
	fmt.Println()
	return nil
}

// covers reports whether a path is at or under a prefix, on segment boundaries so /friendsonly is
// not read as being under /friends.
func covers(prefix, path string) bool {
	if prefix == "/" || prefix == path {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/")
}
