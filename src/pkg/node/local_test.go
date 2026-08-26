package node

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/go-iroh/netaddr"
)

// The whole point of enumerating this machine's addresses is that a peer can dial one. An address
// that means "whichever machine is reading this" cannot be dialled from anywhere else.
func TestOnlyAddressesThatMeanThisMachineAreKept(t *testing.T) {
	for _, bad := range []string{"127.0.0.1", "::1", "169.254.3.4", "fe80::1", "224.0.0.251", "0.0.0.0", "::"} {
		if Dialable(netip.MustParseAddr(bad)) {
			t.Errorf("%s was taken for somewhere a peer can dial", bad)
		}
	}
	for _, good := range []string{"192.168.1.24", "10.147.20.3", "100.68.116.48", "203.0.113.7", "2001:db8::1"} {
		if !Dialable(netip.MustParseAddr(good)) {
			t.Errorf("%s was thrown away", good)
		}
	}
}

// Whatever this machine actually has, none of it may be loopback or link-local, and every one of
// them carries the port the endpoint bound — an address without one is not somewhere to dial.
func TestOwnAddressesAreDialableAtTheBoundPort(t *testing.T) {
	own := OwnAddrs(47777)
	if len(own) == 0 {
		t.Skip("this machine has no addresses of its own")
	}

	for _, at := range own {
		if !Dialable(at.Addr()) {
			t.Errorf("%s is not somewhere a peer can dial", at)
		}
		if at.Port() != 47777 {
			t.Errorf("%s is not at the port that was asked for", at)
		}
	}
}

// A port of zero means the endpoint has not bound anything, and an address without a port is not
// somewhere to dial. Publishing one would hand every peer a candidate that cannot work.
func TestNoPortMeansNothingToPublish(t *testing.T) {
	if own := OwnAddrs(0); len(own) != 0 {
		t.Fatalf("published %v with no port bound", own)
	}
}

// Two machines on one wire have to be able to dial each other. The endpoint publishes only the
// address it bound — unspecified, so dropped — and whatever a relay saw, so unless drop pins its
// own addresses there is nothing on the near side of the relay to try.
func TestANodeAdvertisesTheNetworksItIsOn(t *testing.T) {
	n := startLocal(t)

	own := OwnAddrs(n.Endpoint.LocalAddr().Port())
	if len(own) == 0 {
		t.Skip("this machine has no addresses of its own")
	}

	said := n.Endpoint.Addr().IPAddrs()
	for _, at := range own {
		if indexOf(said, at) < 0 {
			t.Errorf("%s is not in what this node advertises: %v", at, said)
		}
	}
}

// And somebody who would rather not say which networks this machine is on can turn it off.
func TestTurningItOffTakesTheAddressesBackDown(t *testing.T) {
	n := startLocal(t)

	if len(OwnAddrs(n.Endpoint.LocalAddr().Port())) == 0 {
		t.Skip("this machine has no addresses of its own")
	}

	SetDirect(false)
	t.Cleanup(func() { SetDirect(true) })

	n.Pin()

	for _, at := range n.Endpoint.Addr().IPAddrs() {
		if !at.Addr().IsUnspecified() {
			t.Errorf("%s is still being advertised after being turned off", at)
		}
	}
}

// A second drop on this machine cannot have the port the first one holds, so it binds elsewhere and
// nothing that dials this identity arrives at it. That has to be said: on its own it shows up as a
// transport timeout against every peer, which reads as the whole network being down.
func TestASecondNodeSaysThePortIsTaken(t *testing.T) {
	// A daemon already holding the port this identity is reached on.
	daemon, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Skip("no sockets here")
	}
	defer daemon.Close()

	port := daemon.LocalAddr().(*net.UDPAddr).Port
	t.Setenv("DROP_PORT", strconv.Itoa(port))

	second := startLocal(t)
	if second.Own() {
		t.Fatal("a node claimed a port another process is holding")
	}

	wrong := second.Trouble()
	if !strings.Contains(wrong, "DROP_PORT") {
		t.Errorf("it did not name the way out: %q", wrong)
	}
	if !strings.Contains(wrong, strconv.Itoa(port)) {
		t.Errorf("it did not name the port that was taken: %q", wrong)
	}
}

// A node that took the port it wanted has nothing to complain about, or every dial would carry an
// explanation for a problem that is not there.
func TestANodeThatGotItsPortSaysNothing(t *testing.T) {
	t.Setenv("DROP_PORT", "0")

	n := startLocal(t)
	if !n.Own() {
		t.Fatal("a node that asked for any port could not have it")
	}
	if wrong := n.Trouble(); wrong != "" {
		t.Errorf("it complained anyway: %q", wrong)
	}
}

// startLocal brings up a node that talks to nothing: no relay, no lookup, an identity in a
// directory of its own.
func startLocal(t *testing.T) *Node {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	was := Rendezvous()
	SetRendezvous(false)
	t.Cleanup(func() { SetRendezvous(was) })

	if _, err := net.Listen("tcp", "127.0.0.1:0"); err != nil {
		t.Skip("no sockets here")
	}

	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)

	n, err := Start(ctx)
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })

	return n
}

// indexOf keeps the assertions above readable.
func indexOf(all []netip.AddrPort, one netip.AddrPort) int {
	for i, at := range all {
		if at == one {
			return i
		}
	}
	return -1
}

// The range a bridge lives in is the range an overlay lives in.
//
// ZeroTier hands out 172.30.x, which is inside the pool docker is usually blamed for. Deciding by
// address would throw away the one link two machines on that overlay actually share, so what a
// machine made for itself is told by the interface it is on.
func TestAnOverlayIsNotMistakenForABridge(t *testing.T) {
	for _, made := range []string{"docker0", "br-1a2b3c", "virbr0", "veth9f2", "podman1"} {
		if !Virtual(made) {
			t.Errorf("%s was taken for a network somebody else can reach", made)
		}
	}
	for _, real := range []string{"eth0", "eno2", "wlan0", "ztmjffowwq", "wt0", "tailscale0", "enx52ea8e7deb7c"} {
		if Virtual(real) {
			t.Errorf("%s was thrown away as a bridge", real)
		}
	}

	// The address that started this: an overlay both machines are on, in docker's range.
	if !Dialable(netip.MustParseAddr("172.30.0.248")) {
		t.Error("an overlay address was refused for looking like a bridge")
	}
}

// Pinning an address is only half of publishing it.
//
// A pkarr publisher keeps the relay and drops every IP unless it is handed a filter, so a machine
// can enumerate its networks, advertise them to itself, and still tell nobody. What is asserted
// here is that the setting reaches the thing that strips them.
func TestWhatIsPinnedIsWhatIsPublished(t *testing.T) {
	relay, err := netaddr.ParseRelayURL("https://relay.example/")
	if err != nil {
		t.Fatal(err)
	}
	here := []netaddr.TransportAddr{
		netaddr.RelayAddr{URL: relay},
		netaddr.IPAddr{Addr: netip.MustParseAddrPort("192.168.1.24:47777")},
		netaddr.IPAddr{Addr: netip.MustParseAddrPort("172.30.0.96:47777")},
	}

	SetDirect(true)
	if got := Filter()(here); len(got) != len(here) {
		t.Errorf("with direct on, %d of %d addresses would be published", len(got), len(here))
	}

	SetDirect(false)
	got := Filter()(here)
	if len(got) != 1 {
		t.Fatalf("with direct off, %d addresses would be published, expected only the relay", len(got))
	}
	if _, ok := got[0].(netaddr.RelayAddr); !ok {
		t.Errorf("with direct off, what would be published is %T", got[0])
	}

	SetDirect(true)
}
