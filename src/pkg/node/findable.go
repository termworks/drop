package node

import (
	"context"
	"fmt"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
)

// Being found by somebody who has never met you.
//
// Two devices that have paired find each other through the rendezvous, which publishes under a key
// derived from their shared secret so nobody else can look them up. That cannot help the first
// time: there is no secret yet, and making one is the whole point of pairing.
//
// So for that one exchange a device publishes where it is under its own endpoint id, and anybody
// holding a ticket can resolve it. Only while it is offering: when the offer ends the publishing
// stops and the record is left to expire, and drop is back to being findable only by devices that
// already know it.
//
// Resolving is always on. Reading a published address costs its owner nothing, and a device that
// cannot resolve is one that can only ever pair over its own wire.

// lookup is the registry the endpoint resolves through, shared by everything this process starts.
var lookup = &iroh.AddressLookupServices{}

// resolving registers the public lookup, so an endpoint id can be turned into somewhere to dial.
func resolving() (iroh.Option, error) {
	resolver, err := iroh.NewPkarrResolver(iroh.N0DNSPkarrRelayProd, nil)
	if err != nil {
		return nil, err
	}
	lookup.AddResolver(resolver)

	return iroh.WithAddressLookup(lookup), nil
}

// How often the record is written again, and how often the address is looked at.
//
// A pkarr record expires, and an address changes when a laptop moves between networks. Both are
// answered by saying it again, which is cheap: one HTTP PUT to a relay.
//
// The second one matters more than it looks. An endpoint does not know where it is the moment it
// starts: it reports a guess, and a few seconds later a network report replaces it with the relay it
// actually reached. Publishing once at startup writes the guess, and whoever holds the ticket then
// dials somewhere nobody is listening.
const (
	republish = 2 * time.Minute
	settle    = 2 * time.Second
)

// Findable publishes where this device is, under its own id, until ctx ends.
//
// Nothing publishes on its own: the endpoint resolves through the lookup services but never writes
// to them, so what to say and when to say it is drop's to decide.
func Findable(ctx context.Context, n *Node) error {
	// Nothing is published by a device that was told not to tell a relay it exists. The record
	// would go up under this device's own endpoint id, which says that id is alive and what address
	// the machine writing it came from — and with the rendezvous off it carries no relay, so that
	// is the whole of what it says. The code still reaches the wire this device is on.
	if !Rendezvous() {
		return nil
	}

	sk, err := Identity()
	if err != nil {
		return err
	}

	publisher, err := iroh.N0PkarrPublisher(sk, &iroh.PkarrPublisherConfig{AddrFilter: Filter()})
	if err != nil {
		return err
	}

	said := ""
	say := func(whatever bool) {
		// Where this machine is on its own networks, again: a laptop that has moved has different
		// addresses, and the endpoint does not go looking for them.
		n.Pin()

		at := n.Endpoint.Addr()

		now := fmt.Sprint(at.Addrs())
		if !whatever && now == said {
			return
		}
		said = now
		publisher.Publish(dns.EndpointDataFromAddr(at))
	}
	say(true)

	go func() {
		// The publisher has a loop of its own that goes on writing the record every few minutes for
		// as long as it is open. Closing it is what makes an offer that ended stop saying where this
		// device is.
		defer func() { _ = publisher.Close() }()

		slow := time.NewTicker(republish)
		defer slow.Stop()

		watch := time.NewTicker(settle)
		defer watch.Stop()

		moved := Moved(ctx, n)

		for {
			select {
			case <-ctx.Done():
				return
			case <-moved:
				say(false)
			case <-watch.C:
				say(false)
			case <-slow.C:
				say(true)
			}
		}
	}()
	return nil
}

// Moved carries a word every time the endpoint's address changes.
//
// The ticker above only looks every couple of seconds, and only at what the endpoint already
// believes. The endpoint itself knows the instant a network report replaces the address the world
// sees it at — which is what a restart does, because the NAT hands out a different port — so the
// record goes out then rather than whenever the next look happens to fall.
func Moved(ctx context.Context, n *Node) <-chan struct{} {
	told := make(chan struct{}, 1)

	go func() {
		for range n.Endpoint.WatchAddr().Stream(ctx) {
			select {
			case told <- struct{}{}:
			default:
			}
		}
	}()
	return told
}
