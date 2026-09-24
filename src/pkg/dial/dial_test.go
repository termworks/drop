package dial

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

// sighted is a wire that says a device is at whatever addresses it was built with.
type sighted []netip.AddrPort

func (s sighted) Find(ctx context.Context, id node.ID) (netaddr.EndpointAddr, bool) {
	if len(s) == 0 {
		return netaddr.EndpointAddr{}, false
	}
	return node.AddrFor(id, s...), true
}

// rendezvous is a finder that answers with a relay, and says whether it was ever asked.
type rendezvous struct {
	relay string
	asked chan struct{}
}

type acceptedConnection struct {
	conn *iroh.Conn
	err  error
}

func (r *rendezvous) Find(ctx context.Context, entry book.Entry) (netaddr.EndpointAddr, bool) {
	select {
	case r.asked <- struct{}{}:
	default:
	}

	url, err := netaddr.ParseRelayURL(r.relay)
	if err != nil {
		return netaddr.EndpointAddr{}, false
	}
	return netaddr.NewEndpointAddr(entry.ID).WithRelayURL(url), true
}

// A sighting on the wire is unsigned: anybody able to put a datagram on the port can say a device is
// somewhere it is not. Taking one for the answer costs the dial everything else it knew — the
// addresses the book wrote down, and the rendezvous, which is the only step that comes back with a
// relay and so the only one that reaches a device nothing can dial.
func TestAWireSightingDoesNotSilenceTheRendezvous(t *testing.T) {
	n := onlyThisMachine(t)

	entry := book.Entry{Name: "alpha", ID: idFor(9), Addrs: []string{"10.9.9.9:47777"}}
	wire := sighted{netip.MustParseAddrPort("192.168.77.5:47777")}
	moved := &rendezvous{relay: "https://relay.example./", asked: make(chan struct{}, 1)}

	ctx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()

	_, _, err := At(ctx, n, wire, moved, entry, forTesting, nil)
	if err == nil {
		t.Fatal("a dial to a device that is not there came back with a connection")
	}
	for _, want := range []string{"10.9.9.9:47777", "192.168.77.5:47777"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s was never tried: %v", want, err)
		}
	}
	if len(moved.asked) == 0 {
		t.Fatal("the wire answered and the rendezvous was never asked, so a relay was never tried")
	}
}

// onlyThisMachine brings up an endpoint that tells nobody anything, under an identity of its own.
func onlyThisMachine(t *testing.T) *node.Node {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DROP_PORT", "0")

	was := node.Rendezvous()
	node.SetRendezvous(false)
	t.Cleanup(func() { node.SetRendezvous(was) })

	n, err := node.Start(t.Context())
	if err != nil {
		t.Fatalf("starting a node: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })

	return n
}

// An id costs a keypair to mint and anybody may open a connection. Keeping one entry per stranger
// that ever dialled is a map that only grows, and nothing ever looks at those entries: every caller
// asking for a connection hands in an entry read out of the book.
func TestOnlyADeviceTheBookHasIsKept(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)

	held := Hold(nil, nil, nil)

	held.Adopt(idFor(11), forTesting, &iroh.Conn{})
	if len(held.open) != 0 {
		t.Fatalf("a stranger's connection was filed under %v", held.open)
	}

	// And a device paired while this was already running is not a stranger.
	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	pinned.Pair("alpha", idFor(12), []byte("a shared secret"))
	if err := pinned.Save(); err != nil {
		t.Fatal(err)
	}

	held.Adopt(idFor(12), forTesting, &iroh.Conn{})
	if len(held.open) != 1 {
		t.Fatalf("a paired device's connection was not kept: %v", held.open)
	}

	delete(held.open, key(idFor(12), forTesting))
	if err := os.WriteFile(filepath.Join(config, "drop", "peers.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	held.Adopt(idFor(12), forTesting, &iroh.Conn{})
	if len(held.open) != 0 {
		t.Fatalf("an unreadable address book left a stale peer trusted: %v", held.open)
	}
}

func TestADeadHeldConnectionIsReplacedByAnArrival(t *testing.T) {
	t.Setenv("DROP_PORT", "0")
	wasRendezvous := node.Rendezvous()
	node.SetRendezvous(false)
	t.Cleanup(func() { node.SetRendezvous(wasRendezvous) })

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	remote, err := node.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := remote.Close(); err != nil {
			t.Error(err)
		}
	})

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	local, err := node.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := local.Close(); err != nil {
			t.Error(err)
		}
	})

	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	pinned.Pair("remote", remote.ID(), testSharedSecret())
	if err := pinned.Save(); err != nil {
		t.Fatal(err)
	}

	held := Hold(nil, nil, nil)
	firstDial, firstArrival := connectNodes(t, remote, local)
	held.Adopt(remote.ID(), node.ALPNSession, firstArrival)
	if err := firstDial.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstArrival.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("closed connection remained live")
	}

	secondDial, secondArrival := connectNodes(t, remote, local)
	t.Cleanup(func() {
		_ = secondDial.Close()
		_ = secondArrival.Close()
	})
	held.Adopt(remote.ID(), node.ALPNSession, secondArrival)
	if held.open[key(remote.ID(), node.ALPNSession)] != secondArrival {
		t.Fatal("a fresh arrival did not replace the dead held connection")
	}
	if err := secondDial.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondArrival.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("replacement connection remained live after close")
	}
	if held.Reaching(remote.ID()) {
		t.Fatal("a dead held connection was reported as reachable")
	}
	if _, ok := held.open[key(remote.ID(), node.ALPNSession)]; ok {
		t.Fatal("a dead held connection remains stored")
	}
}

func connectNodes(t *testing.T, from, to *node.Node) (*iroh.Conn, *iroh.Conn) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	accepted := make(chan acceptedConnection, 1)
	go func() {
		conn, err := to.Accept(ctx)
		accepted <- acceptedConnection{conn: conn, err: err}
	}()

	dialed, err := from.Dial(ctx, to.Addr(), node.ALPNSession)
	if err != nil {
		t.Fatal(err)
	}
	arrival := <-accepted
	if arrival.err != nil {
		_ = dialed.Close()
		t.Fatal(arrival.err)
	}
	return dialed, arrival.conn
}

func TestServingAnswersAConnectionMadeBeforeTheHandler(t *testing.T) {
	local := onlyThisMachine(t)
	remote := onlyThisMachine(t)

	dialed, arrival := connectNodes(t, local, remote)
	defer func() { _ = arrival.Close() }()

	held := Hold(nil, nil, nil)
	held.keep(remote.ID(), node.ALPNSession, dialed)
	defer held.Close()

	type answer struct {
		from node.ID
		alpn string
	}
	answered := make(chan answer, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	held.Serving(ctx, func(from node.ID, alpn string, stream *iroh.Stream) {
		_ = stream.Close()
		answered <- answer{from: from, alpn: alpn}
	})

	stream, err := arrival.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("opening a stream back over the held connection: %v", err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatalf("starting the stream back over the held connection: %v", err)
	}

	select {
	case got := <-answered:
		if got.from != remote.ID() || got.alpn != node.ALPNSession {
			t.Fatalf("the delayed handler answered %s on %q", got.from, got.alpn)
		}
	case <-ctx.Done():
		t.Fatal("the handler never answered the connection that preceded it")
	}
}

func testSharedSecret() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

// A device that restarted dials again for what it was connected for, and says nothing on the
// connection from before: a stream opened there waits for an answer that never comes. So its
// arrival takes the place of an old connection that still looks alive, and whatever else is held to
// it is dialled afresh rather than trusted.
func TestAnArrivalSupersedesAnOldConnection(t *testing.T) {
	t.Setenv("DROP_PORT", "0")
	wasRendezvous := node.Rendezvous()
	node.SetRendezvous(false)
	t.Cleanup(func() { node.SetRendezvous(wasRendezvous) })

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	remote, err := node.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	local, err := node.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })

	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	pinned.Pair("remote", remote.ID(), testSharedSecret())
	if err := pinned.Save(); err != nil {
		t.Fatal(err)
	}

	held := Hold(nil, nil, nil)
	oldDial, oldArrival := connectNodes(t, remote, local)
	t.Cleanup(func() { _ = oldDial.Close() })
	_, otherArrival := connectNodes(t, remote, local)
	held.Adopt(remote.ID(), node.ALPNSession, oldArrival)
	held.Adopt(remote.ID(), node.ALPNHello, otherArrival)

	// Old enough not to have crossed with anything on the way.
	held.born[oldArrival] = time.Now().Add(-time.Minute)
	held.born[otherArrival] = time.Now().Add(-time.Minute)

	newDial, newArrival := connectNodes(t, remote, local)
	t.Cleanup(func() { _ = newDial.Close() })
	held.Adopt(remote.ID(), node.ALPNSession, newArrival)

	if held.open[key(remote.ID(), node.ALPNSession)] != newArrival {
		t.Fatal("an old connection outlived the arrival that replaced it")
	}
	if held.held(remote.ID(), node.ALPNHello) != nil {
		t.Fatal("a connection from before the device came back was offered again")
	}
}
