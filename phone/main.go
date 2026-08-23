// Command phone is drop on Android.
//
// A node in its own right, not a client of one: it generates its own keypair, keeps its own address
// book, pairs with other devices and dials them over QUIC. What it shows is the same Gio code the
// browser runs, and what it runs underneath is the same Go the command line does.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gioui.org/app"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/gui"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

func main() {
	if err := settle(); err != nil {
		fmt.Fprintln(os.Stderr, "drop:", err)
	}

	ctx := context.Background()

	n, err := node.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "drop: could not start:", err)
		os.Exit(1)
	}

	// Multicast is often filtered on a phone's network, so this may find nothing. It costs little
	// and is the only way to meet a device without an address written down.
	lan, _ := discovery.StartLAN(ctx, n)

	pinned, err := book.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "drop:", err)
	}

	ears := listenTo(ctx, n, pinned, mounts())

	gui.Run(&gui.Direct{
		Node:  n,
		LAN:   lan,
		Reach: reaching(n, lan),

		// Both halves of pairing: a phone is a node like any other, so it can show a code or
		// read one.
		OfferPairing: func() (string, error) { return ears.offer() },
		PairingState: func() (string, string, error) { return ears.pairingState() },
		StopPairing:  func() error { return ears.stopPairing() },
		JoinPairing:  func(ticket string) (string, error) { return joinFrom(ctx, n, lan, ticket) },
	})
}

// settle puts the identity and the conversations where Android lets an app keep things.
//
// The rest of drop reads XDG, which does not exist here, so it is pointed at the directory the
// system gave this app. Without it the keypair would be regenerated on every launch and the phone
// would be a different device each time.
func settle() error {
	where, err := app.DataDir()
	if err != nil {
		return fmt.Errorf("finding somewhere to keep things: %w", err)
	}

	for key, at := range map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(where, "config"),
		"XDG_DATA_HOME":   filepath.Join(where, "data"),
	} {
		if err := os.MkdirAll(at, 0o700); err != nil {
			return fmt.Errorf("making %s: %w", at, err)
		}
		if err := os.Setenv(key, at); err != nil {
			return err
		}
	}
	return nil
}

// reaching is how this device turns a peer into a connection, which is the same ladder every other
// build of drop climbs.
func reaching(n *node.Node, lan *discovery.LAN) func(context.Context, book.Entry, string) (io.Closer, proto.Stream, error) {
	return func(ctx context.Context, to book.Entry, alpn string) (io.Closer, proto.Stream, error) {
		conn, s, err := dial.To(ctx, n, lan, nil, to, alpn)
		if err != nil {
			return nil, nil, err
		}
		return conn, s, nil
	}
}

func inbox() string {
	where, err := app.DataDir()
	if err != nil {
		return os.TempDir()
	}
	at := filepath.Join(where, "inbox")
	_ = os.MkdirAll(at, 0o700)
	return at
}

// The config package is linked so a phone can grow one later without the rest moving.
var _ = conf.FilePath

// mounts is what a phone offers by default: somewhere to talk, and somewhere to put a file.
//
// Open to anyone paired, because pairing is already the deliberate act. Anything more belongs in a
// config, which a phone has no comfortable way to edit yet.
func mounts() *ns.Table {
	open := ns.Access{AnyPaired: true}

	table := ns.NewTable()
	_ = table.Add(ns.Mount{Path: "/chat", Kind: ns.KindChat, Access: open})
	_ = table.Add(ns.Mount{Path: "/inbox", Kind: ns.KindFiles, Dir: inbox(), Access: open})

	return table
}
