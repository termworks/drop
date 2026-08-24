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
		if !onWire && moved != nil {
			if found, ok := moved.Find(ctx, entry); ok {
				at = found
			}
		}
	}

	// The nearest address alone first, when there is one worth singling out. A transport handed
	// every address a machine has does not race them: the one that would answer waits behind the
	// ones that never will, and a device on the same wire takes ten seconds instead of five
	// milliseconds. If that guess is wrong, everything is still tried below.
	if best, ok := nearestOf(at); ok {
		quick, stop := context.WithTimeout(ctx, straightAway)
		conn, s, err := open(quick, n, best, alpn)
		stop()

		if err == nil {
			return conn, s, nil
		}
	}

	conn, s, err := open(ctx, n, at, alpn)

	// A dial resumes a cached TLS session when it has one, and a peer that has restarted since
	// rejects it. That is not a failure, it is the handshake saying to start over: the ticket is
	// spent, so a second dial is a plain one. Once, because a second rejection is a real fault.
	if errors.Is(err, quic.Err0RTTRejected) {
		conn, s, err = open(ctx, n, at, alpn)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reaching %s: %w", entry.Name, err)
	}
	return conn, s, nil
}

// straightAway is how long the nearest address gets on its own.
//
// Long enough for a machine on the same wire, which answers in milliseconds, and short enough that
// being wrong costs less than the full attempt saves.
const straightAway = 3 * time.Second

// nearestOf picks the one address worth trying by itself, if any is.
func nearestOf(at netaddr.EndpointAddr) (netaddr.EndpointAddr, bool) {
	ranked := Nearest(at.IPAddrs())
	if len(ranked) < 2 || !onOurWire(ranked[0].Addr()) {
		// One address is already the whole attempt, and nothing on our own wire means no guess
		// worth making.
		return netaddr.EndpointAddr{}, false
	}
	return node.AddrFor(at.ID, ranked[0]), true
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
