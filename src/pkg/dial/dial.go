// Package dial turns a device you know into a connection to it.
//
// The ladder lives here rather than in a command, because a phone climbs the same one: this wire,
// then — only if it did not answer — a rendezvous, which is the one step that involves anybody
// else, and what the book remembers when neither of them knows.
package dial

import (
	"context"
	"errors"
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
		// What the book remembers is the floor, not the preference: it needs nothing running to
		// resolve, and anything that answers below replaces it outright. A remembered address whose
		// mapping has expired is worse than no address at all — it is a timeout somebody waits for
		// before the address that would have worked is ever tried.
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
		return nil, nil, unreachable(n.Trouble(), entry, at, err)
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

// atMost is how many addresses get a short attempt of their own before the whole set is tried at
// once. Two: a best guess and one alternative, so being wrong twice costs six seconds rather than
// the length of whatever list a peer happens to advertise.
const atMost = 2

// worthTrying is the addresses to try on their own, best first.
//
// Nearest first. An address on our own wire is the only one known to mean the same machine at both
// ends, and it answers in milliseconds; everything further away is a guess that takes the whole
// timeout to disprove. The one that answered last time goes ahead of the others it is equally near
// as, and no further: a remembered address whose NAT mapping has expired, or that now belongs to
// somebody else's machine, would otherwise cost a timeout on every dial before anything else was
// tried.
//
// Only addresses that could plausibly answer straight away are here. The rest are left to the full
// attempt, which has the relay as well and does not spend three seconds each finding out.
func worthTrying(at netaddr.EndpointAddr, entry book.Entry) []netip.AddrPort {
	ranked := Nearest(at.IPAddrs())
	if len(ranked) < 2 {
		// One address is already the whole attempt.
		return nil
	}

	worked, remembered := lastWorked(entry)
	if remembered {
		ranked = promote(ranked, worked)
	}

	var out []netip.AddrPort
	for _, one := range ranked {
		if len(out) == atMost {
			break
		}
		if near(one.Addr()) <= nearbyEnough || (remembered && one == worked) {
			out = append(out, one)
		}
	}
	return out
}

// nearbyEnough is how far away an address may be and still be worth a short attempt of its own:
// our own wire, or any private range, which is where an overlay and a VPN put a device.
const nearbyEnough = 1

// promote moves one address ahead of every address it is equally near as, and no further.
func promote(ranked []netip.AddrPort, one netip.AddrPort) []netip.AddrPort {
	from := indexOf(ranked, one)
	if from < 0 {
		return ranked
	}

	to := 0
	for to < from && near(ranked[to].Addr()) < near(one.Addr()) {
		to++
	}

	out := append([]netip.AddrPort(nil), ranked...)
	copy(out[to+1:], out[to:from])
	out[to] = one
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

// indexOf is where an address sits in a list, or -1.
func indexOf(all []netip.AddrPort, one netip.AddrPort) int {
	for i, at := range all {
		if at == one {
			return i
		}
	}
	return -1
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
