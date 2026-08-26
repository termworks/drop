package dial

import (
	"context"
	"net/netip"
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
	t.Cleanup(func() { n.Close() })

	return n
}

// An id costs a keypair to mint and anybody may open a connection. Keeping one entry per stranger
// that ever dialled is a map that only grows, and nothing ever looks at those entries: every caller
// asking for a connection hands in an entry read out of the book.
func TestOnlyADeviceTheBookHasIsKept(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

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
}
