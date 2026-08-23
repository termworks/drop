package discovery

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

func addrs(t *testing.T, written ...string) []netip.AddrPort {
	t.Helper()

	out := make([]netip.AddrPort, 0, len(written))
	for _, w := range written {
		ap, err := netip.ParseAddrPort(w)
		if err != nil {
			t.Fatalf("parsing %q: %v", w, err)
		}
		out = append(out, ap)
	}
	return out
}

// Encode and decode are two lists of calls that must stay in step, and nothing in the type system
// keeps them there. A field read in the wrong order here means peers silently never find each
// other, which is the hardest kind of failure to attribute.
func TestAnnounceRoundTrips(t *testing.T) {
	want := addrs(t, "192.168.1.10:47901", "10.0.0.4:47901")

	id, got, ok := decodeAnnounce(encodeAnnounce("some-endpoint-id", want))
	if !ok {
		t.Fatal("decodeAnnounce() rejected what encodeAnnounce() produced")
	}
	if id != "some-endpoint-id" {
		t.Fatalf("id = %q", id)
	}
	if len(got) != len(want) {
		t.Fatalf("addrs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("addrs[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// The group carries whatever anyone else puts on it, so anything not drop's is ignored rather than
// misparsed into a bogus peer.
func TestAnnounceIgnoresForeignPackets(t *testing.T) {
	for _, packet := range [][]byte{
		nil,
		[]byte("random noise on the wire"),
		func() []byte {
			w := wire.NewWriter()
			w.String("some-other-tool")
			w.String("an-id")
			return w.Body()
		}(),
	} {
		if _, _, ok := decodeAnnounce(packet); ok {
			t.Errorf("decodeAnnounce() accepted a foreign packet: %q", packet)
		}
	}
}

// A count read off the wire must not be trusted enough to allocate on.
func TestAnnounceRefusesAnAbsurdCount(t *testing.T) {
	w := wire.NewWriter()
	w.String(Magic)
	w.String("an-id")
	w.Uint(1 << 20)

	if _, _, ok := decodeAnnounce(w.Body()); ok {
		t.Fatal("decodeAnnounce() accepted a packet claiming a million addresses")
	}
}

// One unreadable address should not throw away the ones beside it.
func TestAnnounceKeepsTheReadableAddresses(t *testing.T) {
	w := wire.NewWriter()
	w.String(Magic)
	w.String("an-id")
	w.Uint(3)
	w.String("192.168.1.10:47901")
	w.String("not-an-address")
	w.String("10.0.0.4:47901")

	_, got, ok := decodeAnnounce(w.Body())
	if !ok {
		t.Fatal("decodeAnnounce() rejected a packet with one bad address")
	}
	if len(got) != 2 {
		t.Fatalf("addrs = %v, want the two readable ones", got)
	}
}

// A node with nowhere to be reached announces nothing, rather than an empty invitation.
func TestAnnounceIsEmptyWithoutAddresses(t *testing.T) {
	if packet := encodeAnnounce("an-id", nil); len(packet) != 0 {
		t.Fatalf("encodeAnnounce() with no addresses produced %d bytes", len(packet))
	}
}

// A nil LAN is what a caller gets when discovery could not start; it must find nothing rather
// than panic, so callers do not have to special-case it.
func TestNilLANFindsNothing(t *testing.T) {
	var l *LAN

	if _, ok := l.Find(t.Context(), testID(t)); ok {
		t.Fatal("a nil LAN reported finding a peer")
	}
}

// The window has to span an announcement, or a lookup can miss a peer that is announcing normally.
func TestLookupWindowSpansAnAnnouncement(t *testing.T) {
	if LANWindow <= AnnounceEvery {
		t.Fatalf("LANWindow %s must exceed AnnounceEvery %s", LANWindow, AnnounceEvery)
	}
	if Stale <= AnnounceEvery {
		t.Fatalf("Stale %s must exceed AnnounceEvery %s, or a peer expires between announcements", Stale, AnnounceEvery)
	}
}

func TestMagicIsNotEmpty(t *testing.T) {
	if strings.TrimSpace(Magic) == "" {
		t.Fatal("Magic is empty, so any packet on the group would be taken as drop's")
	}
}

// testID mints an id without needing a network.
func testID(t *testing.T) node.ID {
	t.Helper()

	var seed [32]byte
	for i := range seed {
		seed[i] = 7
	}
	return key.NewSecretKey(seed).Public().EndpointID()
}
