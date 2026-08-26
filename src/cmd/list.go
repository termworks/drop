package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// listThere asks a machine what it serves and shows what may be reached.
func listThere(parent context.Context, at ns.Address, entry book.Entry, wait time.Duration) error {
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

	hello, err := serving(find, n, lan, entry)
	if err != nil {
		return err
	}

	shown := make([]proto.Served, 0, len(hello.Serves))
	for _, served := range hello.Serves {
		if covers(at.Path, served.Path) {
			shown = append(shown, served)
		}
	}

	if len(shown) == 0 {
		if at.Path != ns.Root {
			fmt.Printf("\n%s shares nothing with you under %s\n\n", entry.Name, at.Path)
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
