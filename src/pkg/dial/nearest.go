package dial

import (
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
)

// Which of a device's addresses to try, and in what order.
//
// A machine reports every address it has: the wire, the wifi, a VPN, a container bridge, whatever
// a mesh network gave it. Most of them mean nothing from here. Handing the lot to the transport
// does not make it try harder — it makes one dial out of many that cannot work, and the good one
// waits behind them. Measured on two machines on one wire: five addresses took ten seconds and
// failed, the one that was actually reachable took five milliseconds.

// Nearest orders addresses by how likely they are to answer from this machine, dropping the ones
// that cannot.
func Nearest(addrs []netip.AddrPort) []netip.AddrPort {
	out := make([]netip.AddrPort, 0, len(addrs))
	for _, at := range addrs {
		if node.Virtual(at.Addr()) {
			continue
		}
		out = append(out, at)
	}

	sort.SliceStable(out, func(i, j int) bool { return near(out[i].Addr()) < near(out[j].Addr()) })
	return out
}

// near is how close an address is to this machine, smaller being nearer.
func near(ip netip.Addr) int {
	switch {
	case onOurWire(ip):
		// The same subnet as one of our own interfaces. Nothing else is as good a guess: it is
		// the only case where the address is known to mean the same thing on both machines.
		return 0
	case ip.IsPrivate():
		return 1
	case ip.IsLoopback() || ip.IsLinkLocalUnicast():
		return 4
	default:
		// A mesh or carrier-grade range: reachable sometimes, and slow to find out when not.
		return 3
	}
}

// ours is this machine's own subnets, read once: interfaces do change, but not between two dials,
// and reading them is a syscall per address otherwise.
var ours = struct {
	sync.Mutex
	nets []netip.Prefix
	read time.Time
}{}

// onOurWire reports whether an address is on a network this machine is also on.
func onOurWire(ip netip.Addr) bool {
	for _, net := range subnets() {
		if net.Contains(ip) {
			return true
		}
	}
	return false
}

func subnets() []netip.Prefix {
	ours.Lock()
	defer ours.Unlock()

	if time.Since(ours.read) < time.Minute && ours.nets != nil {
		return ours.nets
	}

	found, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	nets := make([]netip.Prefix, 0, len(found))
	for _, at := range found {
		one, ok := at.(*net.IPNet)
		if !ok {
			continue
		}

		ip, ok := netip.AddrFromSlice(one.IP)
		if !ok {
			continue
		}
		bits, _ := one.Mask.Size()
		if bits == 0 {
			continue
		}

		ip = ip.Unmap()
		if node.Virtual(ip) || ip.IsLoopback() {
			continue
		}
		nets = append(nets, netip.PrefixFrom(ip, bits).Masked())
	}

	ours.nets, ours.read = nets, time.Now()
	return nets
}
