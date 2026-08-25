package node

import (
	"context"
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

// republish is how often the record is written again.
//
// A pkarr record expires, and an address changes when a laptop moves between networks. Both are
// answered by saying it again, which is cheap: one HTTP PUT to a relay.
const republish = 2 * time.Minute

// Findable publishes where this device is, under its own id, until ctx ends.
//
// Nothing publishes on its own: the endpoint resolves through the lookup services but never writes
// to them, so what to say and when to say it is drop's to decide.
func Findable(ctx context.Context, n *Node) error {
	sk, err := Identity()
	if err != nil {
		return err
	}

	publisher, err := iroh.N0PkarrPublisher(sk, nil)
	if err != nil {
		return err
	}

	say := func() { publisher.Publish(dns.EndpointDataFromAddr(n.Endpoint.Addr())) }
	say()

	go func() {
		tick := time.NewTicker(republish)
		defer tick.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				say()
			}
		}
	}()
	return nil
}
