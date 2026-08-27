// Package discovery finds where a peer currently is.
package discovery

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/tmc/go-iroh/netaddr"
	"golang.org/x/net/ipv4"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Where drop announces itself on the local network.
//
// Its own group and port rather than mDNS's. Sharing 5353 with a system responder means sharing a
// socket group with it, and on Linux SO_REUSEPORT hands each datagram to one member of that group
// — so announcements are delivered to avahi instead of to drop, most of the time.
const (
	Group = "239.255.77.88"
	Port  = 47800
)

// Magic marks a packet as drop's, so anything else on the group is ignored rather than misparsed.
const Magic = "drop-lan-1"

// AnnounceEvery is how often a node says where it is, so a peer that starts later still hears it.
const AnnounceEvery = 2 * time.Second

// LANWindow is how long to listen for a peer before deciding it is not on this network. Longer
// than AnnounceEvery, so a lookup always spans at least one announcement.
const LANWindow = 5 * time.Second

// Stale is how long an announcement is trusted after it was last heard.
const Stale = 30 * time.Second

// Peers is how many devices this wire is remembered as holding.
//
// Larger than any real network, and small enough that a stream of made-up ids costs a bounded
// amount of memory rather than the machine. An announcement is unsigned and anybody who can put a
// datagram on the port can send one, so the number of them that arrive is not this node's to decide.
const Peers = 512

// LAN is this node's view of the local network.
type LAN struct {
	mu    sync.RWMutex
	peers map[string]sighting
	conn  *net.UDPConn
	// out is the same socket, for saying which interface an announcement goes out of.
	out *ipv4.PacketConn
	// on is every interface that joined the group, which is where announcements are written.
	on   []net.Interface
	self string
}

type sighting struct {
	addrs []netip.AddrPort
	seen  time.Time
}

// StartLAN begins announcing and listening. It stops when ctx is done.
func StartLAN(ctx context.Context, n *node.Node) (*LAN, error) {
	// Turned off on purpose, which is how the path that does not use the local wire gets tested:
	// with this set, finding a device has to go out to a relay and come back.
	if os.Getenv("DROP_NO_MDNS") != "" {
		return nil, fmt.Errorf("the local wire is turned off by DROP_NO_MDNS")
	}

	group := &net.UDPAddr{IP: net.ParseIP(Group), Port: Port}

	lc := net.ListenConfig{Control: reuseAddr}
	pc, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("0.0.0.0:%d", Port))
	if err != nil {
		return nil, fmt.Errorf("listening for local peers: %w", err)
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		_ = pc.Close()
		return nil, fmt.Errorf("listening for local peers: not a UDP socket")
	}

	// Joined on every interface that can, because which one reaches the other device is not
	// knowable here: a laptop on wifi and a desktop on ethernet are the ordinary case.
	p := ipv4.NewPacketConn(conn)
	var joined []net.Interface
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagUp == 0 || ifaces[i].Flags&net.FlagMulticast == 0 {
			continue
		}
		if err := p.JoinGroup(&ifaces[i], group); err == nil {
			joined = append(joined, ifaces[i])
		}
	}
	if len(joined) == 0 {
		if err := p.JoinGroup(nil, group); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("joining the local group: %w", err)
		}
	}
	_ = p.SetMulticastLoopback(true)

	lan := &LAN{peers: map[string]sighting{}, conn: conn, out: p, on: joined, self: n.ID().String()}

	go lan.listen(ctx)
	go lan.announce(ctx, n, group)

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	return lan, nil
}

// announce says where this node is, on a tick.
func (l *LAN) announce(ctx context.Context, n *node.Node, group *net.UDPAddr) {
	tick := time.NewTicker(AnnounceEvery)
	defer tick.Stop()

	for {
		// Rebuilt each time, so a node that changed network announces where it is now rather than
		// repeating where it used to be.
		if packet := encodeAnnounce(l.self, LocalAddrs(n)); len(packet) > 0 {
			l.say(packet, group)
		}

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// say writes one copy of an announcement per interface the group was joined on.
//
// A socket bound to 0.0.0.0 emits one copy, on whichever interface the route table picks for the
// group — the default route. A laptop on wifi with a cable to a desktop announces only on the wifi,
// and a machine whose default route is a tunnel announces only into the tunnel, while both of them
// listen on everything. So the interface is named per write rather than left to the route.
func (l *LAN) say(packet []byte, group *net.UDPAddr) {
	if len(l.on) == 0 {
		_, _ = l.conn.WriteToUDP(packet, group)
		return
	}

	for i := range l.on {
		if err := l.out.SetMulticastInterface(&l.on[i]); err != nil {
			continue
		}
		_, _ = l.out.WriteTo(packet, nil, group)
	}
}

// listen records what other nodes announce.
func (l *LAN) listen(ctx context.Context) {
	buf := make([]byte, 4096)

	for {
		n, from, err := l.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}

		l.heard(buf[:n], from.Addr().Unmap())
	}
}

// heard writes down one announcement, if it is one worth believing.
//
// Two things are asked of it, and neither of them is who sent it: the id has to be an id, so that a
// packet cannot make up map keys, and the addresses have to include the one the packet arrived
// from, so that a machine can say where it is and not where somebody else is. Beyond that an
// announcement stays a hint — the dial that follows is what proves anything.
func (l *LAN) heard(packet []byte, from netip.Addr) {
	id, addrs, ok := decodeAnnounce(packet)
	if !ok || id == l.self {
		return
	}
	if _, err := node.ParseID(id); err != nil {
		return
	}
	if !claims(addrs, from) {
		return
	}

	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, seen := l.peers[id]; !seen && len(l.peers) >= Peers {
		for at, old := range l.peers {
			if now.Sub(old.seen) >= Stale {
				delete(l.peers, at)
			}
		}
		if len(l.peers) >= Peers {
			return
		}
	}
	l.peers[id] = sighting{addrs: addrs, seen: now}
}

// claims reports whether an announcement includes the address it came from.
func claims(addrs []netip.AddrPort, from netip.Addr) bool {
	for _, at := range addrs {
		if at.Addr() == from {
			return true
		}
	}
	return false
}

// Find asks the local network where a peer is, giving up after LANWindow.
//
// A nil LAN simply never finds anything, so a caller that could not bring discovery up does not
// have to special-case it.
func (l *LAN) Find(parent context.Context, id node.ID) (netaddr.EndpointAddr, bool) {
	if l == nil {
		return netaddr.EndpointAddr{}, false
	}

	want := id.String()
	deadline := time.Now().Add(LANWindow)

	for {
		l.mu.RLock()
		seen, ok := l.peers[want]
		l.mu.RUnlock()

		if ok && time.Since(seen.seen) < Stale {
			return node.AddrFor(id, seen.addrs...), true
		}
		if time.Now().After(deadline) {
			return netaddr.EndpointAddr{}, false
		}

		select {
		case <-parent.Done():
			return netaddr.EndpointAddr{}, false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// encodeAnnounce packs who this node is and where it can be reached.
//
// Unsigned on purpose: the only thing an announcement decides is which address to try, and the
// connection itself proves the far end holds the key. A forged address costs a failed dial, not a
// wrong peer.
func encodeAnnounce(id string, addrs []netip.AddrPort) []byte {
	if len(addrs) == 0 {
		return nil
	}

	w := wire.NewWriter()
	w.String(Magic)
	w.String(id)
	w.Uint(uint64(len(addrs)))
	for _, a := range addrs {
		w.String(a.String())
	}
	return w.Body()
}

// maxAddrs bounds what one announcement may claim, so a packet cannot make the reader allocate on
// a number it chose.
const maxAddrs = 32

func decodeAnnounce(packet []byte) (string, []netip.AddrPort, bool) {
	r := wire.NewReader(packet)

	magic, err := r.String(len(Magic))
	if err != nil || magic != Magic {
		return "", nil, false
	}
	id, err := r.String(256)
	if err != nil {
		return "", nil, false
	}
	count, err := r.Uint()
	if err != nil || count > maxAddrs {
		return "", nil, false
	}

	addrs := make([]netip.AddrPort, 0, count)
	for i := uint64(0); i < count; i++ {
		written, err := r.String(64)
		if err != nil {
			return "", nil, false
		}
		ap, err := netip.ParseAddrPort(written)
		if err != nil {
			continue
		}
		addrs = append(addrs, ap)
	}
	return id, addrs, true
}

// LocalAddrs is where this node can be reached on this network: its own interface addresses at the
// port the endpoint bound, which is what an announcement carries and what a pairing writes down.
func LocalAddrs(n *node.Node) []netip.AddrPort {
	return node.OwnAddrs(n.Endpoint.LocalAddr().Port())
}
