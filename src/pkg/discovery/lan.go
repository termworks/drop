// Package discovery finds where a peer currently is.
package discovery

import (
	"context"
	"fmt"
	"net"
	"net/netip"
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

// LAN is this node's view of the local network.
type LAN struct {
	mu    sync.RWMutex
	peers map[string]sighting
	conn  *net.UDPConn
	self  string
}

type sighting struct {
	addrs []netip.AddrPort
	seen  time.Time
}

// StartLAN begins announcing and listening. It stops when ctx is done.
func StartLAN(ctx context.Context, n *node.Node) (*LAN, error) {
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
	joined := 0
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagUp == 0 || ifaces[i].Flags&net.FlagMulticast == 0 {
			continue
		}
		if err := p.JoinGroup(&ifaces[i], group); err == nil {
			joined++
		}
	}
	if joined == 0 {
		if err := p.JoinGroup(nil, group); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("joining the local group: %w", err)
		}
	}
	_ = p.SetMulticastLoopback(true)

	lan := &LAN{peers: map[string]sighting{}, conn: conn, self: n.ID().String()}

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
			_, _ = l.conn.WriteToUDP(packet, group)
		}

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// listen records what other nodes announce.
func (l *LAN) listen(ctx context.Context) {
	buf := make([]byte, 4096)

	for {
		n, _, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}

		id, addrs, ok := decodeAnnounce(buf[:n])
		if !ok || id == l.self || len(addrs) == 0 {
			continue
		}

		l.mu.Lock()
		l.peers[id] = sighting{addrs: addrs, seen: time.Now()}
		l.mu.Unlock()
	}
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

// LocalAddrs is where this node can be reached on this network.
//
// A freshly bound endpoint advertises nothing — it has not learned its own addresses yet — so on
// the local network they are taken from the interfaces directly, paired with the port it bound.
func LocalAddrs(n *node.Node) []netip.AddrPort {
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
