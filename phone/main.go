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

	// A phone answers as well as asks: pairing, and whatever it declares, are served from here.
	go serve(ctx, n, pinned)

	gui.Run(&gui.Direct{
		Node:  n,
		LAN:   lan,
		Reach: reaching(n, lan),
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

// serve answers what other devices ask of this one.
//
// A phone that could only reach out would be half a device: the point of it having an identity is
// that a workstation can send to it too.
func serve(ctx context.Context, n *node.Node, pinned *book.Book) {
	mounts := ns.NewTable()

	// What a phone offers by default: somewhere to talk, and somewhere to put a file. Anything more
	// belongs in a config, which a phone has no comfortable way to edit yet.
	_ = mounts.Add(ns.Mount{Path: "/chat", Kind: ns.KindChat, Access: ns.Access{AnyPaired: true}})
	_ = mounts.Add(ns.Mount{
		Path:   "/inbox",
		Kind:   ns.KindFiles,
		Dir:    inbox(),
		Access: ns.Access{AnyPaired: true},
	})

	for {
		conn, err := n.Endpoint.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		go answer(conn, n, pinned, mounts)
	}
}

func answer(conn *iroh.Conn, n *node.Node, pinned *book.Book, mounts *ns.Table) {
	defer conn.Close()

	from := conn.RemoteID()

	for {
		s, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}

		go func(s *iroh.Stream) {
			defer s.Close()

			switch conn.ALPN() {
			case node.ALPNHello:
				_ = proto.AnswerHello(s, proto.Hello{
					Name:    node.DisplayName(),
					Version: "phone",
					Serves:  proto.Describe(mounts, whoIs(pinned, from)),
				})

			case node.ALPNSession:
				_ = proto.Handle(s, from, proto.Policy{
					Mounts:  mounts,
					Dir:     inbox(),
					Allow:   func(node.ID, proto.Open) (bool, string) { return true, "" },
					Who:     func(id node.ID) ns.Caller { return whoIs(pinned, id) },
					Message: keeping(pinned),
				})
			}
		}(s)
	}
}

func whoIs(pinned *book.Book, from node.ID) ns.Caller {
	who := ns.Caller{ID: from.String()}

	if pinned != nil {
		if entry, ok := pinned.ByID(from); ok {
			who.Name, who.Paired = entry.Name, entry.Paired()
		}
	}
	return who
}

// keeping stores what arrives, so a message sent while the phone was asleep is there when it wakes.
func keeping(pinned *book.Book) func(node.ID, convo.Message) error {
	return func(from node.ID, m convo.Message) error {
		store, err := convo.Open(from)
		if err != nil {
			return err
		}
		_, err = store.Add(m)
		return err
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
