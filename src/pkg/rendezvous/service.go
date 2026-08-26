package rendezvous

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

// Relay is where records are written and read. The pkarr relay only ever sees derived identities,
// so it learns how many records exist but not whose they are.
const Relay = iroh.N0DNSPkarrRelayProd

// Refresh is how often the current address set is republished.
const Refresh = 5 * time.Minute

// Service keeps this device findable by the devices it has paired with.
//
// It is off unless drop.rendezvous is turned on, because it writes to a relay this machine does
// not own, and that is not something to start doing on a user's behalf without being asked.
type Service struct {
	node *node.Node

	mu         sync.Mutex
	publishers map[string]*iroh.PkarrPublisher
	resolver   *iroh.PkarrResolver
	relay      string
}

// New builds a service that publishes to relay, or to the default relay when it is empty.
func New(n *node.Node, relay string) (*Service, error) {
	if relay == "" {
		relay = Relay
	}
	resolver, err := iroh.NewPkarrResolver(relay, nil)
	if err != nil {
		return nil, fmt.Errorf("the rendezvous relay: %w", err)
	}
	return &Service{
		node:       n,
		publishers: make(map[string]*iroh.PkarrPublisher),
		resolver:   resolver,
		relay:      relay,
	}, nil
}

// Settle is how often the address is looked at again.
//
// An endpoint does not know where it is the moment it starts: it reports a guess, and a few seconds
// later a network report replaces it with the relay it actually reached and the address the world
// sees. Publishing once at startup writes the guess, and every peer then dials somewhere nobody is
// listening until the next refresh -- which is minutes away, and looks exactly like being offline.
const Settle = 2 * time.Second

// Run publishes this device's address to every paired peer until ctx ends.
//
// Twice over: whenever the address changes, which is what catches the moment it settles, and on a
// slow tick regardless, because a published record expires.
func (s *Service) Run(ctx context.Context) {
	slow := time.NewTicker(Refresh)
	defer slow.Stop()

	watch := time.NewTicker(Settle)
	defer watch.Stop()

	defer s.closeAll()

	// Turned off on purpose, which is how the other direction gets tested: a device that publishes
	// nowhere cannot be found, and anything that reaches it has to be a connection it opened.
	if os.Getenv("DROP_NO_PUBLISH") != "" {
		<-ctx.Done()
		return
	}

	said := ""
	say := func(whatever bool) {
		// Where this machine is on its own networks, again: a laptop that has moved has different
		// addresses, and the endpoint does not go looking for them.
		s.node.Pin()

		now := whereNow(s.node.Endpoint.Addr())
		if !whatever && now == said {
			return
		}
		said = now
		s.publishRound(time.Now())
	}

	say(true)

	moved := node.Moved(ctx, s.node)

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
}

// whereNow is an address as something comparable, so a change can be noticed.
func whereNow(at netaddr.EndpointAddr) string {
	out := make([]string, 0, 4)
	for _, one := range at.Addrs() {
		out = append(out, fmt.Sprint(one))
	}
	sort.Strings(out)

	return strings.Join(out, " ")
}

// publishRound writes this device's current address once per pair, per epoch.
//
// One record per pair is the whole point: a single shared record would be one identity that every
// peer, and the relay, could watch.
func (s *Service) publishRound(now time.Time) {
	b, err := book.Load()
	if err != nil {
		return
	}

	data := dns.EndpointDataFromAddr(s.node.Endpoint.Addr())

	live := make(map[string]bool)
	for _, entry := range b.Paired() {
		for _, epoch := range PublishEpochs(now) {
			sk, err := Derive(entry.Secret, s.node.ID(), epoch)
			if err != nil {
				continue
			}

			at := sk.Public().EndpointID().String()
			live[at] = true

			s.mu.Lock()
			p, ok := s.publishers[at]
			if !ok {
				p, err = iroh.NewPkarrPublisher(sk, s.relay, nil)
				if err != nil {
					s.mu.Unlock()
					continue
				}
				s.publishers[at] = p
			}
			s.mu.Unlock()

			p.Publish(data)
		}
	}

	s.retire(live)
}

// retire stops publishers whose epoch has passed, so an old identity stops being refreshed rather
// than being kept alive forever alongside the current one.
func (s *Service) retire(live map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for at, p := range s.publishers {
		if !live[at] {
			_ = p.Close()
			delete(s.publishers, at)
		}
	}
}

func (s *Service) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for at, p := range s.publishers {
		_ = p.Close()
		delete(s.publishers, at)
	}
}

// Find asks the relay where a paired peer currently is.
//
// The returned address carries the peer's real identity, not the derived one: the derived identity
// exists only to name the record, and dialling it would reach nothing.
func (s *Service) Find(ctx context.Context, entry book.Entry) (netaddr.EndpointAddr, bool) {
	if !entry.Paired() {
		return netaddr.EndpointAddr{}, false
	}

	for _, epoch := range ResolveEpochs(time.Now()) {
		sk, err := Derive(entry.Secret, entry.ID, epoch)
		if err != nil {
			continue
		}

		for item, err := range s.resolver.Resolve(ctx, sk.Public().EndpointID()) {
			if err != nil {
				continue
			}
			if addr, ok := rebind(item.EndpointInfo(), entry.ID); ok {
				return addr, true
			}
		}
	}
	return netaddr.EndpointAddr{}, false
}

// rebind moves the addresses out of a record and onto the identity they actually belong to.
func rebind(info dns.EndpointInfo, real node.ID) (netaddr.EndpointAddr, bool) {
	addrs := info.Addr().Addrs()
	if len(addrs) == 0 {
		return netaddr.EndpointAddr{}, false
	}
	return netaddr.NewEndpointAddr(real, addrs...), true
}

// Direct pulls the plain IP addresses out of an endpoint address, for callers that dial by them.
func Direct(addr netaddr.EndpointAddr) []netip.AddrPort {
	var out []netip.AddrPort
	for _, a := range addr.Addrs() {
		if ip, ok := a.(netaddr.IPAddr); ok {
			out = append(out, ip.Addr)
		}
	}
	return out
}
