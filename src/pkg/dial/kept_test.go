package dial

import (
	"context"

	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
	"net/netip"
	"testing"
)

// The one that is on our own wire goes first. Everything else is a guess, and a transport handed a
// guess first spends its timeout on it before trying the address that was always going to work.
func TestTheAddressOnOurOwnWireComesFirst(t *testing.T) {
	// Whatever this machine's networks are, an address inside one of them must outrank an address
	// outside all of them.
	nets := subnets()
	if len(nets) == 0 {
		t.Skip("this machine has no ordinary networks to compare against")
	}

	near := nets[0].Addr().Next()
	far := netip.MustParseAddr("203.0.113.7")

	ranked := Nearest([]netip.AddrPort{
		netip.AddrPortFrom(far, 47777),
		netip.AddrPortFrom(near, 47777),
	})

	if len(ranked) != 2 || ranked[0].Addr() != near {
		t.Fatalf("ranked %v, want the one on our own wire first", ranked)
	}
}

// A bridge every machine has is the same address here as there, so dialling it reaches whatever is
// running on the machine doing the dialling.
func TestVirtualBridgesAreDropped(t *testing.T) {
	ranked := Nearest([]netip.AddrPort{
		netip.MustParseAddrPort("192.168.122.1:47777"),
		netip.MustParseAddrPort("172.18.0.1:47777"),
		netip.MustParseAddrPort("192.168.1.175:47777"),
	})

	if len(ranked) != 1 || ranked[0].Addr().String() != "192.168.1.175" {
		t.Fatalf("ranked %v, want only the real address", ranked)
	}
}

// Ranking must not lose anything that is not a bridge: a device on a mesh network is reachable
// through it, just more slowly than one on the same wire.
func TestNothingElseIsThrownAway(t *testing.T) {
	given := []netip.AddrPort{
		netip.MustParseAddrPort("100.68.116.48:47777"),
		netip.MustParseAddrPort("10.8.67.112:47777"),
		netip.MustParseAddrPort("203.0.113.7:47777"),
	}

	if ranked := Nearest(given); len(ranked) != len(given) {
		t.Fatalf("ranked %v, want all %d kept", ranked, len(given))
	}
}

// A finder that is not there must not be called.
//
// An interface holding a nil pointer is not nil. Handing one over used to take the program down —
// and only when the local wire failed to find the device, which is to say the first time somebody
// carried a laptop to a different network.
func TestANilFinderIsNotCalled(t *testing.T) {
	var missing *neverFinds

	if usable(missing) {
		t.Error("a nil pointer in an interface was taken for a finder")
	}
	if usable(nil) {
		t.Error("nothing at all was taken for a finder")
	}
	if !usable(&neverFinds{}) {
		t.Error("a real finder was taken for nothing")
	}
}

// neverFinds panics if anything calls it, which is what the old code did by itself.
type neverFinds struct{ called bool }

func (f *neverFinds) Find(context.Context, book.Entry) (netaddr.EndpointAddr, bool) {
	f.called = true
	return netaddr.EndpointAddr{}, false
}
