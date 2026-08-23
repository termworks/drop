package cmd

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"sync"

	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/rendezvous"
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
	// rendezvousFor is consulted lazily, so a command that never needs one never builds it.
	var moved dial.Finder
	if rv := rendezvousFor(n); rv != nil {
		moved = rv
	}
	return dial.At(ctx, n, lan, moved, entry, alpn, known)
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

// startRendezvous begins publishing this device's address, when the config asked for it.
//
// Returns nil when it is off, and every caller handles a nil service, so the feature being off is
// not a special case anyone has to remember.
func startRendezvous(ctx context.Context, n *node.Node) *rendezvous.Service {
	if !node.Rendezvous() {
		return nil
	}

	svc, err := rendezvous.New(n, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: rendezvous unavailable: %v\n", err)
		return nil
	}

	setRendezvous(svc)
	go svc.Run(ctx)
	return svc
}

// The running service, so a dial can consult it without every call site having to carry one.
// A short-lived command never sets it and dials without a rendezvous, which is correct: publishing
// takes a process that stays up long enough to be worth finding.
var (
	rendezvousMu sync.Mutex
	rendezvousOn *rendezvous.Service
)

func setRendezvous(s *rendezvous.Service) {
	rendezvousMu.Lock()
	defer rendezvousMu.Unlock()

	rendezvousOn = s
}

// rendezvousFor returns something able to resolve, whether or not this process publishes.
//
// A one-shot command has nothing worth publishing and exits before a record would be useful,
// but it still has to be able to find a peer that moved. Resolving needs only the relay.
func rendezvousFor(n *node.Node) *rendezvous.Service {
	rendezvousMu.Lock()
	defer rendezvousMu.Unlock()

	if rendezvousOn != nil {
		return rendezvousOn
	}
	if !node.Rendezvous() {
		return nil
	}

	svc, err := rendezvous.New(n, "")
	if err != nil {
		return nil
	}
	rendezvousOn = svc
	return svc
}
