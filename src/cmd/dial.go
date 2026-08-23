package cmd

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
)

// reach opens a stream to a peer for one protocol.
//
// Where the peer is comes from the local network first, because that is instant and needs nothing;
// iroh's own relay routing is what carries the rest.
func reach(ctx context.Context, n *node.Node, lan *discovery.LAN, entry book.Entry, alpn string) (*iroh.Conn, *iroh.Stream, error) {
	return reachAt(ctx, n, lan, entry, alpn, nil)
}

// reachAt is reach with addresses the caller already knows, which is how a ticket works: it
// carries where the far end is so the first connection needs nothing to resolve it.
func reachAt(ctx context.Context, n *node.Node, lan *discovery.LAN, entry book.Entry, alpn string, known []netip.AddrPort) (*iroh.Conn, *iroh.Stream, error) {
	at := node.AddrFor(entry.ID, known...)
	if len(known) == 0 {
		// What the book remembers comes first: it was learned at pairing and needs nothing
		// running to resolve it. mDNS is the fallback for a peer that has moved.
		if remembered := parseAddrs(entry.Addrs); len(remembered) > 0 {
			at = node.AddrFor(entry.ID, remembered...)
		}
		if found, ok := lan.Find(ctx, entry.ID); ok {
			at = found
		}
	}
	conn, err := n.Dial(ctx, at, alpn)
	if err != nil {
		return nil, nil, fmt.Errorf("reaching %s: %w", entry.Name, err)
	}
	s, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("opening a stream to %s: %w", entry.Name, err)
	}
	return conn, s, nil
}

// serveLoop accepts connections and routes each by the protocol it negotiated.
//
// ALPN is per connection in iroh rather than per stream, so the protocol is known before a byte is
// read and every stream on a connection belongs to the same one.
func serveLoop(ctx context.Context, n *node.Node, handlers map[string]func(node.ID, *iroh.Stream)) {
	for {
		conn, err := n.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		go serveConn(ctx, conn, handlers)
	}
}

func serveConn(ctx context.Context, conn *iroh.Conn, handlers map[string]func(node.ID, *iroh.Stream)) {
	defer conn.Close()

	handle, ok := handlers[conn.ALPN()]
	if !ok {
		return
	}
	from := conn.RemoteID()

	for {
		s, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		go handle(from, s)
	}
}

// parseAddrs reads what the address book wrote down, skipping anything unreadable.
func parseAddrs(written []string) []netip.AddrPort {
	out := make([]netip.AddrPort, 0, len(written))
	for _, text := range written {
		ap, err := netip.ParseAddrPort(text)
		if err != nil {
			continue
		}
		out = append(out, ap)
	}
	return out
}
