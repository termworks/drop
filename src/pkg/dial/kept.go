package dial

import (
	"context"
	"sync"

	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
)

// Kept holds open connections, one per device and protocol, so reaching a device costs the finding
// and the handshake once rather than every time.
//
// Finding a device is the expensive part: local discovery, a relay, or a rendezvous, then a QUIC
// handshake. On a good wire that is milliseconds; on a bad one it is tens of seconds. Doing it
// again for every message a person types is what makes a chat feel like a form submission. QUIC
// multiplexes, so a held connection costs one socket and carries as many streams as are asked of it.
type Kept struct {
	node *node.Node
	lan  *discovery.LAN
	find Finder

	mu   sync.Mutex
	open map[string]*iroh.Conn
}

func Hold(n *node.Node, lan *discovery.LAN, find Finder) *Kept {
	return &Kept{node: n, lan: lan, find: find, open: map[string]*iroh.Conn{}}
}

// To opens a stream to a device, over the connection already held if there is one.
//
// The stream is the caller's to close. The connection is not: it stays for the next one.
func (k *Kept) To(ctx context.Context, entry book.Entry, alpn string) (*iroh.Stream, error) {
	if conn := k.held(entry.ID, alpn); conn != nil {
		if s, err := conn.OpenStreamSync(ctx); err == nil {
			return s, nil
		}
		// It was held but is no longer good. Forgotten, and dialled again below.
		k.drop(entry.ID, alpn)
	}

	conn, s, err := To(ctx, k.node, k.lan, k.find, entry, alpn)
	if err != nil {
		return nil, err
	}

	k.keep(entry.ID, alpn, conn)
	return s, nil
}

// held is the live connection for a device, or nil.
func (k *Kept) held(id node.ID, alpn string) *iroh.Conn {
	k.mu.Lock()
	defer k.mu.Unlock()

	conn, ok := k.open[key(id, alpn)]
	if !ok {
		return nil
	}

	// A connection whose context is done is closed, however it got that way.
	select {
	case <-conn.Context().Done():
		delete(k.open, key(id, alpn))
		return nil
	default:
		return conn
	}
}

func (k *Kept) keep(id node.ID, alpn string, conn *iroh.Conn) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if was, ok := k.open[key(id, alpn)]; ok && was != conn {
		was.Close()
	}
	k.open[key(id, alpn)] = conn
}

func (k *Kept) drop(id node.ID, alpn string) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if conn, ok := k.open[key(id, alpn)]; ok {
		conn.Close()
		delete(k.open, key(id, alpn))
	}
}

// Close lets go of everything held.
func (k *Kept) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()

	for at, conn := range k.open {
		conn.Close()
		delete(k.open, at)
	}
}

func key(id node.ID, alpn string) string { return id.String() + "\x00" + alpn }
