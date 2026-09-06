package rendezvous

import (
	"context"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
)

// Finding a device by its own name, for the one exchange that cannot use the rendezvous.
//
// Everything else drop does looks a device up under a key derived from a shared secret, so a relay
// never learns who is asking for whom. Pairing has no secret yet -- making one is the point -- so
// for that exchange, and only that one, a device is looked up under the endpoint id written in its
// ticket. Whoever holds the ticket already knows the id.
//
// This exists because iroh's own resolution hands the dialler direct addresses and drops the relay,
// which is the only address a device behind NAT has. Resolving here keeps it.
type Openly struct {
	resolver iroh.AddressResolver
	retryMin time.Duration
	retryMax time.Duration
}

const (
	openlyRetryMin = 250 * time.Millisecond
	openlyRetryMax = 5 * time.Second
)

// Open makes a finder that looks a device up under its own id.
func Open() (*Openly, error) {
	resolver, err := iroh.NewPkarrResolver(Relay, nil)
	if err != nil {
		return nil, err
	}
	return &Openly{
		resolver: resolver,
		retryMin: openlyRetryMin,
		retryMax: openlyRetryMax,
	}, nil
}

// Find looks for wherever that device last said it was.
func (o *Openly) Find(ctx context.Context, entry book.Entry) (netaddr.EndpointAddr, bool) {
	retryMin, retryMax := o.retryMin, o.retryMax
	if retryMin <= 0 {
		retryMin = openlyRetryMin
	}
	if retryMax < retryMin {
		retryMax = retryMin
	}

	for delay := retryMin; ; delay = min(2*delay, retryMax) {
		if addr, found := o.findOnce(ctx, entry); found {
			return addr, true
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return netaddr.EndpointAddr{}, false
		case <-timer.C:
		}
	}
}

func (o *Openly) findOnce(ctx context.Context, entry book.Entry) (netaddr.EndpointAddr, bool) {
	for item, err := range o.resolver.Resolve(ctx, entry.ID) {
		if err != nil {
			continue
		}
		if addr, ok := rebind(item.EndpointInfo(), entry.ID); ok {
			return addr, true
		}
	}
	return netaddr.EndpointAddr{}, false
}
