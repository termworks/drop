package node

import (
	"net"
	"net/netip"
	"strings"
)

// Where this machine is on its own networks.
//
// An endpoint knows three things about where it is: the address it bound, which is unspecified and
// so not somewhere anybody can dial; whatever a relay saw it arrive from; and its home relay. It
// never looks at the interfaces it is actually on. Two machines on one wire, or on one overlay,
// therefore publish nothing either of them can dial and meet through a relay somewhere else.
//
// So drop enumerates them itself and pins them into what the endpoint advertises. A pinned address
// survives a network report and is offered as a NAT traversal candidate, which is what turns a
// link that answers in milliseconds into the path the connection takes.

// OwnAddrs is this machine's own unicast addresses at port.
//
// Loopback and link-local are left out: they name whichever machine is reading them, so a peer
// dialling one reaches itself. So is anything on an interface that is down. Port zero gives
// nothing, because an address with no port is not somewhere to dial.
func OwnAddrs(port uint16) []netip.AddrPort {
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
		if Virtual(iface.Name) {
			continue
		}
		here, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, a := range here {
			one, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(one.IP)
			if !ok {
				continue
			}
			if ip = ip.Unmap(); !Dialable(ip) {
				continue
			}
			out = append(out, netip.AddrPortFrom(ip, port))
		}
	}
	return out
}

// Dialable reports whether an address means the same machine from somewhere else.
func Dialable(ip netip.Addr) bool {
	switch {
	case !ip.IsValid(), ip.IsUnspecified(), ip.IsLoopback():
		return false
	case ip.IsLinkLocalUnicast(), ip.IsMulticast():
		return false
	}
	return true
}

// Virtual reports whether an interface is one a machine made for itself.
//
// A bridge that libvirt, docker or podman stood up carries an address every host running the same
// software has, so publishing it sends a peer at whatever its own machine is running, and a hole
// punched there is punched into its own bridge. The interface says so; the address does not.
// Docker's pool is 172.16/12, and so is where ZeroTier usually puts an overlay — the range that
// looks most like a private bridge is the one most worth publishing.
func Virtual(name string) bool {
	for _, made := range []string{"docker", "br-", "virbr", "veth", "podman", "cni", "flannel", "kube"} {
		if strings.HasPrefix(name, made) {
			return true
		}
	}
	return false
}

// Pin puts this machine's own addresses into what the endpoint advertises and takes away the ones
// it no longer has. It reports whether anything changed.
//
// Worth calling again whenever the machine might have moved: a laptop joins another network, an
// overlay comes up, a VPN goes down, and what is worth publishing moves with it. With SetDirect
// off it pins nothing and removes whatever it pinned before.
func (n *Node) Pin() bool {
	var now []netip.AddrPort
	if Direct() {
		now = OwnAddrs(n.Endpoint.LocalAddr().Port())
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	changed := false
	for _, at := range now {
		if !among(n.pinned, at) {
			n.Endpoint.AddExternalAddr(at)
			changed = true
		}
	}
	for _, was := range n.pinned {
		if !among(now, was) {
			n.Endpoint.RemoveExternalAddr(was)
			changed = true
		}
	}

	n.pinned = now
	return changed
}

func among(all []netip.AddrPort, one netip.AddrPort) bool {
	for _, at := range all {
		if at == one {
			return true
		}
	}
	return false
}
