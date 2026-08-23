// Package discovery finds where a peer currently is.
package discovery

import (
	"context"
	"fmt"
	"time"

	"net"
	"net/netip"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh/mdns"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/node"
)

// ServiceName namespaces drop's announcements on the local network.
const ServiceName = "drop"

// LANWindow is how long to listen before deciding a peer is not on this network.
const LANWindow = 3 * time.Second

// LAN announces this node on the local network and answers where a peer is.
//
// iroh resolves by endpoint id rather than by an arbitrary key, which suits drop: the thing being
// looked up is always a device already in the address book.
type LAN struct {
	disc *mdns.Discovery
}

// StartWindow bounds bringing LAN discovery up.
//
// go-iroh v0.1.0's mDNS Start blocks indefinitely when another instance is already listening on
// the host, so it is never waited on without a bound: a device that cannot announce locally should
// fall back to being reached the ordinary way, not fail to start at all.
const StartWindow = 10 * time.Second

// StartLAN begins announcing and listening. It stops when ctx is done.
//
// Returns nil when discovery could not be brought up, which callers treat as "no local network
// shortcut" rather than an error.
func StartLAN(ctx context.Context, n *node.Node) (*LAN, error) {
	disc := mdns.New(n.ID(), mdns.WithServiceName(ServiceName))

	ready := make(chan error, 1)
	go func() { ready <- disc.Start(ctx) }()

	select {
	case err := <-ready:
		if err != nil {
			return nil, fmt.Errorf("starting mDNS: %w", err)
		}
	case <-time.After(StartWindow):
		return nil, fmt.Errorf("mDNS did not come up within %s", StartWindow)
	}

	// What this node publishes is where it can be reached on this network.
	disc.Publish(dns.NewEndpointData().WithIPAddrs(localAddrs(n)...))

	return &LAN{disc: disc}, nil
}

// Find asks the local network where a peer is, giving up after LANWindow.
//
// A nil LAN simply never finds anything, so a caller that could not bring discovery up does not
// have to special-case it.
func (l *LAN) Find(parent context.Context, id node.ID) (netaddr.EndpointAddr, bool) {
	if l == nil {
		return netaddr.EndpointAddr{}, false
	}

	ctx, cancel := context.WithTimeout(parent, LANWindow)
	defer cancel()

	for item, err := range l.disc.Resolve(ctx, id) {
		if err != nil {
			continue
		}
		// The item already carries a whole address, so nothing has to be rebuilt from parts.
		at := item.Addr()
		if !at.IsEmpty() {
			return at, true
		}
	}
	return netaddr.EndpointAddr{}, false
}

// localAddrs is where this node can be reached on this network.
//
// A freshly bound endpoint advertises nothing — it has not learned its own addresses yet — so on
// the local network they are taken from the interfaces directly, paired with the port it bound.
func localAddrs(n *node.Node) []netip.AddrPort {
	port := n.Endpoint.LocalAddr().Port()
	if port == 0 {
		return nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var out []netip.AddrPort
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			prefix, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(prefix.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			if !ip.Is4() {
				continue
			}
			out = append(out, netip.AddrPortFrom(ip, port))
		}
	}
	return out
}

// LocalAddrs is where this node can be reached on this network, for putting in a ticket.
func LocalAddrs(n *node.Node) []netip.AddrPort {
	return localAddrs(n)
}
