package dial

import (
	"context"
	"fmt"
	"strings"
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
	// ctx bounds the accept loops on connections we made.
	ctx context.Context
	// serve is what answers streams the far end opens on a connection we made.
	//
	// A connection is only served by the side that accepted it, so without this a device we
	// dialled cannot say anything back on the same pipe -- it opens a stream and nobody is
	// listening. That is the whole of what makes a device behind a NAT reachable.
	serve func(node.ID, string, *iroh.Stream)
}

func Hold(n *node.Node, lan *discovery.LAN, find Finder) *Kept {
	return &Kept{node: n, lan: lan, find: find, open: map[string]*iroh.Conn{}}
}

// Serving says what to do with streams the far end opens on a connection we made. Without it those
// streams are never accepted, and a device we dialled can only ever answer, never ask.
func (k *Kept) Serving(ctx context.Context, answer func(node.ID, string, *iroh.Stream)) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.serve, k.ctx = answer, ctx
}

// answerOn accepts whatever the far end opens on a connection we made.
func (k *Kept) answerOn(conn *iroh.Conn) {
	k.mu.Lock()
	answer, ctx := k.serve, k.ctx
	k.mu.Unlock()

	if answer == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	from, alpn := conn.RemoteID(), conn.ALPN()
	for {
		s, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		go answer(from, alpn, s)
	}
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

	if was, ok := k.open[key(id, alpn)]; ok && was != conn {
		was.Close()
	}
	k.open[key(id, alpn)] = conn
	answering := k.serve != nil

	k.mu.Unlock()

	// We made this one, so nothing else is accepting on it. Whatever the far end opens here is
	// ours to answer, and it is how a device that cannot be dialled says anything at all.
	if answering {
		go k.answerOn(conn)
	}
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

// Reaching reports whether a connection to a device is being held.
//
// Not a probe: this says a connection exists and was good the last time it was used, which is what
// makes it worth showing. Dialling everybody in the address book to draw a list would spend a
// handshake per device per redraw.
func (k *Kept) Reaching(id node.ID) bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	for at := range k.open {
		if strings.HasPrefix(at, id.String()+"\x00") {
			return true
		}
	}
	return false
}

// Adopt takes a connection somebody else opened to us and keeps it like one we made.
//
// A device behind a NAT that nothing can dial can still dial out. When it does, the connection it
// opened is a way back to it that exists for as long as it holds it — and QUIC does not care which
// side opened it, so a stream can be started in either direction on the same pipe.
//
// Without this, a queue for such a device never empties: whatever is waiting is waiting for a dial
// that cannot succeed, while the device itself is connected and idle.
func (k *Kept) Adopt(id node.ID, alpn string, conn *iroh.Conn) {
	if conn == nil {
		return
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	// Only when nothing is held. Two devices that can both dial will each open one, and replacing
	// a working connection with the other side's would have them closing each other's every time
	// round — which is a conversation that stops mid-sentence every few seconds.
	//
	// A connection that has stopped working is dropped when it is next used, so keeping the older
	// one costs nothing but one failed stream.
	if _, ok := k.open[key(id, alpn)]; ok {
		return
	}
	k.open[key(id, alpn)] = conn
}

// Reach makes sure a connection to a device exists, without asking it anything.
//
// A device nothing can dial has to be the one that dials, and it has to do so before it has
// something to say: a connection opened only when a message is written is a connection that does
// not exist while somebody on the other side is writing one. Holding it open is what makes a
// conversation work in both directions when only one of them can be reached.
func (k *Kept) Reach(ctx context.Context, entry book.Entry, alpn string) error {
	if conn := k.held(entry.ID, alpn); conn != nil {
		return nil
	}

	conn, s, err := To(ctx, k.node, k.lan, k.find, entry, alpn)
	if err != nil {
		return err
	}

	// The stream was only the excuse to open the connection. The connection is the point.
	_ = s.Close()

	k.keep(entry.ID, alpn, conn)
	return nil
}

// Existing opens a stream only on a connection already held, and refuses rather than dialling.
//
// For pushing down a connection somebody else opened. Dialling there would be a loop: their
// connection makes us reach for them, ours makes them reach for us, and two devices that can both
// dial would spend their time opening connections at each other.
func (k *Kept) Existing(ctx context.Context, entry book.Entry, alpn string) (*iroh.Stream, error) {
	conn := k.held(entry.ID, alpn)
	if conn == nil {
		return nil, fmt.Errorf("no connection to %s is being held", entry.Name)
	}

	s, err := conn.OpenStreamSync(ctx)
	if err != nil {
		k.drop(entry.ID, alpn)
		return nil, err
	}
	return s, nil
}
