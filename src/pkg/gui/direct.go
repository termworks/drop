//go:build android

package gui

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Direct is a device that is a drop node itself.
//
// This is what a phone gets: its own keypair, its own address book, dialling other devices over
// QUIC. A browser cannot have one — it cannot open a UDP socket — which is the whole reason Remote
// exists beside it.
type Direct struct {
	Node *node.Node
	LAN  *discovery.LAN

	// Reach is how a peer is turned into a connection. Supplied rather than built here, so the
	// resolution ladder stays in one place instead of being written twice.
	Reach func(ctx context.Context, to book.Entry, alpn string) (io.Closer, proto.Stream, error)
}

func (d *Direct) Self() (Identity, error) {
	who := Identity{Name: node.DisplayName(), ID: d.Node.ID().String()}

	for _, at := range discovery.LocalAddrs(d.Node) {
		who.Addrs = append(who.Addrs, at.String())
	}
	return who, nil
}

func (d *Direct) Peers() ([]Peer, error) {
	pinned, err := book.Load()
	if err != nil {
		return nil, err
	}

	out := make([]Peer, 0, len(pinned.All()))
	for _, e := range pinned.All() {
		waiting := 0
		if store, err := convo.Open(e.ID); err == nil {
			if pending, err := store.Pending(); err == nil {
				waiting = len(pending)
			}
		}
		out = append(out, Peer{Name: e.Name, ID: e.ID.String(), Paired: e.Paired(), Unread: waiting})
	}
	return out, nil
}

func (d *Direct) Spaces(peer string) ([]Space, error) {
	entry, err := book.Resolve(peer)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, s, err := d.Reach(ctx, entry, node.ALPNHello)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	hello, err := proto.AskHello(s)
	if err != nil {
		return nil, err
	}

	out := make([]Space, 0, len(hello.Serves))
	for _, served := range hello.Serves {
		out = append(out, Space{Path: served.Path, Kind: served.Kind.String(), Writable: served.Writable})
	}
	return out, nil
}

func (d *Direct) Log(peer string) ([]Message, error) {
	entry, err := book.Resolve(peer)
	if err != nil {
		return nil, err
	}
	store, err := convo.Open(entry.ID)
	if err != nil {
		return nil, err
	}
	history, err := store.History()
	if err != nil {
		return nil, err
	}

	out := make([]Message, 0, len(history))
	for _, m := range history {
		out = append(out, Message{
			ID: m.ID, Kind: kindName(m.Kind), Mine: m.Dir == convo.Out,
			Body: m.Body, Extra: m.Extra, At: m.At,
		})
	}
	return out, nil
}

func kindName(kind byte) string {
	switch kind {
	case convo.KindLink:
		return "link"
	case convo.KindFile:
		return "file"
	case convo.KindEvent:
		return "event"
	default:
		return "text"
	}
}

// Say queues a message and tries to deliver it. Queued first, so a device that is asleep is not an
// error — it is something that will go out when it wakes.
func (d *Direct) Say(peer, body string, asLink bool) error {
	entry, err := book.Resolve(peer)
	if err != nil {
		return err
	}

	kind := convo.KindText
	if asLink {
		kind = convo.KindLink
	}

	store, err := convo.Open(entry.ID)
	if err != nil {
		return err
	}
	if _, err := store.Add(convo.Message{Dir: convo.Out, Kind: kind, Body: body}); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	conn, s, err := d.Reach(ctx, entry, node.ALPNSession)
	if err != nil {
		return err
	}
	defer conn.Close()

	pending, err := store.Pending()
	if err != nil {
		return err
	}

	sent, err := proto.SendMessages(ctx, s, "/chat", pending, node.DisplayName())
	if err != nil {
		return err
	}
	return store.Delivered(sent...)
}

func (d *Direct) Watch(peer, path string, into io.Writer, resize func(cols, rows int), done <-chan struct{}) error {
	entry, err := book.Resolve(peer)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-done
		cancel()
	}()

	conn, s, err := d.Reach(ctx, entry, node.ALPNSession)
	if err != nil {
		return err
	}
	defer conn.Close()

	duplex, err := proto.OpenDuplex(ctx, s, path, path, node.DisplayName())
	if err != nil {
		return err
	}
	if resize != nil {
		duplex.OnResize = func(cols, rows uint16) { resize(int(cols), int(rows)) }
	}

	// Nothing is typed back: a viewer that could type would be a shell handed to whoever picked up
	// the phone.
	_ = duplex.Close()

	err = duplex.Pump(into)
	if err != nil && strings.Contains(err.Error(), "context canceled") {
		return nil
	}
	return err
}
