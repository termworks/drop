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
	return dial.At(ctx, n, lan, finder(n), entry, alpn, known)
}

// finder is the rendezvous as something to look a device up with, or nothing at all.
//
// Nothing at all has to be a nil interface, not an interface holding a nil pointer. rendezvousFor
// returns a typed nil when there is no rendezvous, and handing that straight over makes a value
// that is not nil and cannot be called: the dial checks it, finds something, and dereferences
// nothing. It only shows up when the local wire fails to find the device — which is to say, the
// first time somebody carries a laptop to another network.
func finder(n *node.Node) dial.Finder {
	if rv := rendezvousFor(n); rv != nil {
		return rv
	}
	return nil
}

// serveLoop accepts connections and routes each by the protocol it negotiated.
//
// ALPN is per connection in iroh rather than per stream, so the protocol is known before a byte is
// read and every stream on a connection belongs to the same one.
func serveLoop(ctx context.Context, n *node.Node, handlers map[string]func(node.ID, *iroh.Stream)) {
	serveLoopKeeping(ctx, n, handlers, nil, nil)
}

// serveLoopKeeping is the same, and keeps every connection that arrives.
//
// A device behind a NAT that nothing can dial can still dial out, and the connection it opens is a
// way back to it for as long as it holds it. Keeping it means whatever is queued for that device
// can go down the same pipe instead of waiting for a dial that will never succeed.
func serveLoopKeeping(
	ctx context.Context,
	n *node.Node,
	handlers map[string]func(node.ID, *iroh.Stream),
	held *dial.Kept,
	arrived func(node.ID),
) {
	for {
		conn, err := n.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		if held != nil {
			held.Adopt(conn.RemoteID(), conn.ALPN(), conn)
		}
		if arrived != nil {
			go arrived(conn.RemoteID())
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

// listener is the one accept loop an endpoint gets.
//
// Two loops on one endpoint do not divide the work, they race for it: whichever wins a connection
// decides, and one that does not know the protocol hangs up on it. That is a pairing refused for no
// reason a person could see, so there is one loop and handlers are added to it.
type listener struct {
	mu       sync.Mutex
	handlers map[string]func(node.ID, *iroh.Stream)
}

func listenOn(ctx context.Context, n *node.Node, handlers map[string]func(node.ID, *iroh.Stream)) *listener {
	if handlers == nil {
		handlers = map[string]func(node.ID, *iroh.Stream){}
	}
	l := &listener{handlers: handlers}

	go func() {
		for {
			conn, err := n.Accept(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			go l.answer(ctx, conn)
		}
	}()

	return l
}

// Handle adds a protocol, or takes one away when given nil — which is how a pairing stops being
// offered the moment it is finished with.
func (l *listener) Handle(alpn string, handle func(node.ID, *iroh.Stream)) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if handle == nil {
		delete(l.handlers, alpn)
		return
	}
	l.handlers[alpn] = handle
}

func (l *listener) answer(ctx context.Context, conn *iroh.Conn) {
	defer conn.Close()

	l.mu.Lock()
	handle, ok := l.handlers[conn.ALPN()]
	l.mu.Unlock()

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
