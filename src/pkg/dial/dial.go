// Package dial turns a device you know into a connection to it.
//
// The ladder lives here rather than in a command, because a phone climbs the same one: what this
// wire says and what the book remembers, neither of which costs anybody anything, and then — only
// if nothing there answered — a rendezvous, which is the one step that involves somebody else and
// the only one that comes back with a relay.
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
	"github.com/bresilla/drop/src/pkg/node"
)

// Finder is something that knows where a device moved to. Nil is fine: it means nobody is asked.
type Finder interface {
	Find(ctx context.Context, entry book.Entry) (netaddr.EndpointAddr, bool)
}

// Wire is the local network, which knows where a device is while it is on the same one. Nil is
// fine, and so is a discovery.LAN that never started: both mean nothing is heard.
type Wire interface {
	Find(ctx context.Context, id node.ID) (netaddr.EndpointAddr, bool)
}

// To reaches a device and opens a stream on it.
func To(ctx context.Context, n *node.Node, wire Wire, moved Finder, entry book.Entry, alpn string) (*iroh.Conn, *iroh.Stream, error) {
	return At(ctx, n, wire, moved, entry, alpn, nil)
}

// At is the same, with addresses the caller already has — which is how pairing works, before there
// is anything written down to look up.
func At(ctx context.Context, n *node.Node, wire Wire, moved Finder, entry book.Entry, alpn string, known []netip.AddrPort) (*iroh.Conn, *iroh.Stream, error) {
	at := node.AddrFor(entry.ID, known...)

	if len(known) == 0 {
		// What the book wrote down and what the wire says are the addresses nobody had to be asked
		// for, and neither outranks the other. The book needs nothing running to resolve; the wire
		// answers in milliseconds but is unsigned, so anybody able to put a datagram on the port can
		// put an address here. Both are guesses, so both go in the same set and the ladder below
		// sorts them by how near they are.
		nearby := Addrs(entry.Addrs)
		if wire != nil {
			if seen, ok := wire.Find(ctx, entry.ID); ok {
				nearby = append(nearby, seen.IPAddrs()...)
			}
		}
		at = node.AddrFor(entry.ID, nearby...)

		if usable(moved) {
			// Tried before anybody is asked, because a peer standing next to you is reached without
			// telling a relay anything about it — and for a bounded time, because a wrong address
			// here has to cost a wait and not the connection. The rendezvous is the only step that
			// comes back with a relay, and it has to still happen.
			if len(nearby) > 0 {
				soon, stop := context.WithTimeout(ctx, nearbyWait)
				conn, s, err := climb(soon, n, entry, at, alpn)
				stop()

				if err == nil {
					return conn, s, nil
				}
			}

			if found, ok := moved.Find(ctx, entry); ok {
				// Added to what is already known rather than put in its place. A rendezvous record
				// carries the relay, which is the only path to a device nothing can dial; the
				// addresses carry the paths that answer straight away. Taking either one for the
				// answer throws away the other.
				at = alsoAt(found, nearby...)
			}
		}
	}

	conn, s, err := climb(ctx, n, entry, at, alpn)
	if err != nil {
		return nil, nil, unreachable(n.Trouble(), entry, at, err)
	}
	return conn, s, nil
}

// nearbyWait is how long the addresses nobody had to be asked for get before somebody is.
//
// Every short attempt and one on the whole set. A device that is really at one of them answers in
// milliseconds; past this it is not there, and what remains to try is the relay.
const nearbyWait = straightAway * (atMost + 1)

// climb tries the nearest addresses one at a time and then everything at once.
//
// One address at a time first, best guess downwards. A transport handed every address a machine has
// does not race them: the one that would answer waits behind the ones that never will, and a device
// on the same wire takes ten seconds instead of five milliseconds. If every guess is wrong, the
// whole set is still tried after.
func climb(ctx context.Context, n *node.Node, entry book.Entry, at netaddr.EndpointAddr, alpn string) (*iroh.Conn, *iroh.Stream, error) {
	for _, best := range worthTrying(at, entry) {
		quick, stop := context.WithTimeout(ctx, straightAway)
		conn, s, err := openFresh(quick, n, node.AddrFor(entry.ID, best), alpn)
		stop()

		if err == nil {
			remember(entry, best)
			return conn, s, nil
		}
	}
	return openFresh(ctx, n, at, alpn)
}

// alsoAt adds addresses to one already built, keeping whatever else it carries.
func alsoAt(at netaddr.EndpointAddr, more ...netip.AddrPort) netaddr.EndpointAddr {
	for _, one := range more {
		at = at.WithIP(one)
	}
	return at
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
