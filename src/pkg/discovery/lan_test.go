package discovery

import (
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"
	"golang.org/x/net/ipv4"

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

// idFrom mints an id without needing a network, one per seed.
func idFrom(seed int) node.ID {
	var raw [32]byte
	raw[0], raw[1] = byte(seed), byte(seed>>8)
	return key.NewSecretKey(raw).Public().EndpointID()
}

// An announcement is unsigned, and anybody able to put a datagram on the port can send one. The
// most it can honestly say is where the machine that sent it is: one that names addresses it is not
// at is a stranger answering for somebody else, and believing it points every dial at whatever it
// chose.
func TestASightingSaysWhereItsSenderIs(t *testing.T) {
	peer := idFrom(2).String()
	l := &LAN{peers: map[string]sighting{}, self: idFrom(1).String()}

	// The peer itself, on two networks at once, announcing over one of them.
	honest := encodeAnnounce(peer, addrs(t, "192.168.1.50:47777", "10.0.0.5:47777"))
	l.heard(honest, netip.MustParseAddr("192.168.1.50"))

	if got := l.peers[peer].addrs; len(got) != 2 {
		t.Fatalf("an honest announcement was written down as %v, want both of its addresses", got)
	}

	// Somebody else on the wire, saying the same peer is wherever it likes.
	forged := encodeAnnounce(peer, addrs(t, "203.0.113.9:1234", "203.0.113.10:1234"))
	l.heard(forged, netip.MustParseAddr("198.51.100.7"))

	got := l.peers[peer].addrs
	if len(got) != 2 || got[0] != netip.MustParseAddrPort("192.168.1.50:47777") {
		t.Fatalf("a stranger's announcement replaced the peer's own with %v", got)
	}
}

// An id off the wire becomes a map key, so it has to be an id. Otherwise a packet chooses what this
// node remembers, and 256 bytes of anything is a new entry.
func TestASightingNeedsARealID(t *testing.T) {
	l := &LAN{peers: map[string]sighting{}, self: idFrom(1).String()}

	junk := strings.Repeat("x", 256)
	l.heard(encodeAnnounce(junk, addrs(t, "192.168.1.50:47777")), netip.MustParseAddr("192.168.1.50"))

	if len(l.peers) != 0 {
		t.Fatalf("%d peers were written down for a packet whose id is not an id", len(l.peers))
	}
}

// Ids cost a keypair to mint, and nothing but the wire decides how many arrive. What is remembered
// has to have a ceiling, or a stream of announcements is the daemon's memory.
func TestTheWireIsRememberedUpToAPoint(t *testing.T) {
	l := &LAN{peers: map[string]sighting{}, self: idFrom(1).String()}

	from := netip.MustParseAddr("192.168.1.50")
	packet := addrs(t, "192.168.1.50:47777")
	for i := range Peers * 2 {
		l.heard(encodeAnnounce(idFrom(i+2).String(), packet), from)
	}

	if len(l.peers) > Peers {
		t.Fatalf("%d peers were written down, want no more than %d", len(l.peers), Peers)
	}
}

// A machine listens on every interface it has and has to announce on every one too. A socket bound
// to 0.0.0.0 emits a single copy, on whichever interface the route table picks for the group: a
// laptop on wifi with a cable to a desktop never announces down the cable, and one whose default
// route is a tunnel never announces on a wire at all.
func TestAnnouncingGoesOutEveryInterfaceJoined(t *testing.T) {
	on := multicasting()
	if len(on) < 2 {
		t.Skip("this machine has one network to announce on")
	}

	// The group is heard on all of them at once, so every copy that was written arrives here.
	hear, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Skipf("no socket to listen on: %v", err)
	}
	defer hear.Close()

	group := &net.UDPAddr{IP: net.ParseIP(Group), Port: hear.LocalAddr().(*net.UDPAddr).Port}
	joined := ipv4.NewPacketConn(hear.(*net.UDPConn))
	on = joinedOn(joined, on, group)
	if len(on) < 2 {
		t.Skipf("only %d interfaces would join the group", len(on))
	}

	say, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Skipf("no socket to announce from: %v", err)
	}
	defer say.Close()

	out := ipv4.NewPacketConn(say.(*net.UDPConn))
	_ = out.SetMulticastLoopback(true)

	l := &LAN{conn: say.(*net.UDPConn), out: out, on: on}
	l.say(encodeAnnounce(idFrom(3).String(), addrs(t, "192.168.1.50:47777")), group)

	if copies := heardFrom(hear); copies != len(on) {
		t.Fatalf("%d copies of one announcement arrived, want one per interface (%d)", copies, len(on))
	}
}

// multicasting is every interface an announcement could go out of.
func multicasting() []net.Interface {
	ifaces, _ := net.Interfaces()

	var out []net.Interface
	for _, one := range ifaces {
		if one.Flags&net.FlagUp == 0 || one.Flags&net.FlagMulticast == 0 {
			continue
		}
		here, _ := one.Addrs()
		for _, a := range here {
			if at, ok := a.(*net.IPNet); ok && at.IP.To4() != nil {
				out = append(out, one)
				break
			}
		}
	}
	return out
}

func joinedOn(p *ipv4.PacketConn, on []net.Interface, group *net.UDPAddr) []net.Interface {
	var out []net.Interface
	for i := range on {
		if err := p.JoinGroup(&on[i], group); err == nil {
			out = append(out, on[i])
		}
	}
	return out
}

// heardFrom counts what arrived, stopping when nothing more does.
func heardFrom(conn net.PacketConn) int {
	buf := make([]byte, 4096)

	seen := 0
	for {
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if _, _, err := conn.ReadFrom(buf); err != nil {
			return seen
		}
		seen++
	}
}
