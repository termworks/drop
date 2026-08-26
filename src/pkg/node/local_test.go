package node

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

// The whole point of enumerating this machine's addresses is that a peer can dial one. An address
// that means "whichever machine is reading this" cannot be dialled from anywhere else.
func TestOnlyAddressesThatMeanThisMachineAreKept(t *testing.T) {
	for _, bad := range []string{"127.0.0.1", "::1", "169.254.3.4", "fe80::1", "224.0.0.251", "0.0.0.0", "::"} {
		if dialable(netip.MustParseAddr(bad)) {
			t.Errorf("%s was taken for somewhere a peer can dial", bad)
		}
	}
	for _, good := range []string{"192.168.1.24", "10.147.20.3", "100.68.116.48", "203.0.113.7", "2001:db8::1"} {
		if !dialable(netip.MustParseAddr(good)) {
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
		if !dialable(at.Addr()) {
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
