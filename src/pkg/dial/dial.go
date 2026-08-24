// Package dial turns a device you know into a connection to it.
//
// The ladder lives here rather than in a command, because a phone climbs the same one: what the book
// remembers, then this wire, then — only if neither answered — a rendezvous, which is the one step
// that involves anybody else.
package dial

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
)

// Finder is something that knows where a device moved to. Nil is fine: it means nobody is asked.
type Finder interface {
	Find(ctx context.Context, entry book.Entry) (netaddr.EndpointAddr, bool)
}

// To reaches a device and opens a stream on it.
func To(ctx context.Context, n *node.Node, lan *discovery.LAN, moved Finder, entry book.Entry, alpn string) (*iroh.Conn, *iroh.Stream, error) {
	return At(ctx, n, lan, moved, entry, alpn, nil)
}

// At is the same, with addresses the caller already has — which is how pairing works, before there
// is anything written down to look up.
func At(ctx context.Context, n *node.Node, lan *discovery.LAN, moved Finder, entry book.Entry, alpn string, known []netip.AddrPort) (*iroh.Conn, *iroh.Stream, error) {
	at := node.AddrFor(entry.ID, known...)

	if len(known) == 0 {
		// What the book remembers comes first: it was learned at pairing and needs nothing running
		// to resolve it. It can also be stale, which is what the other two are for.
		if remembered := Addrs(entry.Addrs); len(remembered) > 0 {
			at = node.AddrFor(entry.ID, remembered...)
		}

		onWire := false
		if lan != nil {
			if found, ok := lan.Find(ctx, entry.ID); ok {
				at, onWire = found, true
			}
		}

		// Only when this wire did not answer, because it is the one step that asks a third party. A
		// peer standing next to you is reached without telling a relay anything about it.
		if !onWire && usable(moved) {
			if found, ok := moved.Find(ctx, entry); ok {
				at = found
			}
		}
	}

	// One address at a time first, best guess downwards. A transport handed every address a
	// machine has does not race them: the one that would answer waits behind the ones that never
	// will, and a device on the same wire takes ten seconds instead of five milliseconds. If every
	// guess is wrong, the whole set is still tried below.
	for _, best := range worthTrying(at, entry) {
		quick, stop := context.WithTimeout(ctx, straightAway)
		conn, s, err := openFresh(quick, n, node.AddrFor(entry.ID, best), alpn)
		stop()

		if err == nil {
			remember(entry, best)
			return conn, s, nil
		}
	}

	conn, s, err := openFresh(ctx, n, at, alpn)
	if err != nil {
		return nil, nil, fmt.Errorf("reaching %s: %w", entry.Name, err)
	}
	return conn, s, nil
}

// openFresh dials, starting over when the far end refuses a resumed session.
//
// A dial reuses a cached TLS session when it has one, and a device that has restarted since rejects
// it. That is not a failure: it is the handshake saying to start again without the ticket. It says
// so once per spent ticket, and there can be more than one cached, so this tries a few times rather
// than handing somebody the words "0-RTT rejected" — which describe a detail of the transport and
// nothing they can act on.
func openFresh(ctx context.Context, n *node.Node, at netaddr.EndpointAddr, alpn string) (*iroh.Conn, *iroh.Stream, error) {
	const spentTickets = 3

	var conn *iroh.Conn
	var s *iroh.Stream
	var err error

	for range spentTickets {
		conn, s, err = open(ctx, n, at, alpn)
		if !errors.Is(err, quic.Err0RTTRejected) {
			return conn, s, err
		}
	}
	return conn, s, err
}

// straightAway is how long the nearest address gets on its own.
//
// Long enough for a machine on the same wire, which answers in milliseconds, and short enough that
// being wrong costs less than the full attempt saves.
const straightAway = 3 * time.Second

// worthTrying is the addresses to try on their own, best first.
//
// The one that answered last time comes first: finding a device is expensive and the answer rarely
// changes between two conversations. Then the one on our own wire, which is the best guess when
// there is no memory to go on.
func worthTrying(at netaddr.EndpointAddr, entry book.Entry) []netip.AddrPort {
	candidates := at.IPAddrs()
	if len(candidates) < 2 {
		// One address is already the whole attempt.
		return nil
	}

	var out []netip.AddrPort

	if worked, ok := lastWorked(entry); ok && has(candidates, worked) {
		out = append(out, worked)
	}

	ranked := Nearest(candidates)
	if len(ranked) > 0 && onOurWire(ranked[0].Addr()) && !has(out, ranked[0]) {
		out = append(out, ranked[0])
	}
	return out
}

// lastWorked is the address this device answered on last time, if one was written down.
func lastWorked(entry book.Entry) (netip.AddrPort, bool) {
	if len(entry.Addrs) == 0 {
		return netip.AddrPort{}, false
	}

	at, err := netip.ParseAddrPort(entry.Addrs[0])
	return at, err == nil
}

func has(all []netip.AddrPort, one netip.AddrPort) bool {
	for _, at := range all {
		if at == one {
			return true
		}
	}
	return false
}

// remember writes down the address that answered, so the next conversation starts there.
//
// Quietly: a conversation is not the moment to complain that a file could not be written, and the
// only cost of failing is doing the finding again next time.
func remember(entry book.Entry, at netip.AddrPort) {
	pinned, err := book.Load()
	if err != nil {
		return
	}
	_, _ = pinned.Reached(entry.ID, at.String())
}

// open dials and takes a stream, waiting for the handshake first.
//
// The wait is what makes a rejected session show up here rather than halfway through somebody's
// conversation: until the handshake finishes, a stream opened on a resumed connection looks fine
// and fails on the first read.
func open(ctx context.Context, n *node.Node, at netaddr.EndpointAddr, alpn string) (*iroh.Conn, *iroh.Stream, error) {
	conn, err := n.Dial(ctx, at, alpn)
	if err != nil {
		return nil, nil, err
	}

	select {
	case <-conn.HandshakeComplete():
	case <-ctx.Done():
		conn.Close()
		return nil, nil, ctx.Err()
	}

	s, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, s, nil
}

// Addrs reads the addresses a book wrote down, dropping any it cannot make sense of rather than
// refusing the lot.
func Addrs(written []string) []netip.AddrPort {
	out := make([]netip.AddrPort, 0, len(written))
	for _, at := range written {
		if parsed, err := netip.ParseAddrPort(at); err == nil {
			out = append(out, parsed)
		}
	}
	return out
}

// usable reports whether a finder is really there.
//
// An interface holding a nil pointer is not nil, and calling through it panics. That is a mistake a
// caller makes rather than a state worth having, but it costs one reflection to survive rather than
// taking the program down in the one situation this code exists for: a device that has moved.
func usable(f Finder) bool {
	if f == nil {
		return false
	}

	at := reflect.ValueOf(f)
	switch at.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
		return !at.IsNil()
	}
	return true
}
