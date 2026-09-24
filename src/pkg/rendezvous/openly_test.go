package rendezvous

import (
	"context"
	"iter"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

func TestOpenPairingWaitsForPublication(t *testing.T) {
	peer := id(7)
	want := netip.MustParseAddrPort("192.0.2.7:47777")
	var attempts atomic.Int32

	resolver := iroh.AddressResolverFunc(func(context.Context, node.ID) iter.Seq2[iroh.Item, error] {
		return func(yield func(iroh.Item, error) bool) {
			if attempts.Add(1) < 3 {
				return
			}
			resolved := netaddr.NewEndpointAddr(peer, netaddr.IPAddr{Addr: want})
			yield(iroh.NewItem(dns.EndpointInfoFromAddr(resolved), "test", nil), nil)
		}
	})

	open := &Openly{resolver: resolver, retryMin: time.Millisecond, retryMax: 2 * time.Millisecond}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	got, found := open.Find(ctx, book.Entry{ID: peer})
	if !found {
		t.Fatal("the pairing record was not found after it appeared")
	}
	if direct := Direct(got); len(direct) != 1 || direct[0] != want {
		t.Fatalf("resolved %v, want %v", direct, want)
	}
	if calls := attempts.Load(); calls != 3 {
		t.Fatalf("resolved after %d attempts, want 3", calls)
	}
}

func TestOpenPairingStopsWaitingWithItsContext(t *testing.T) {
	var attempts atomic.Int32
	resolver := iroh.AddressResolverFunc(func(context.Context, node.ID) iter.Seq2[iroh.Item, error] {
		return func(func(iroh.Item, error) bool) { attempts.Add(1) }
	})

	open := &Openly{resolver: resolver, retryMin: time.Millisecond, retryMax: 2 * time.Millisecond}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, found := open.Find(ctx, book.Entry{ID: id(7)}); found {
		t.Fatal("an unpublished pairing record was found")
	}
	if calls := attempts.Load(); calls > 1 {
		t.Fatalf("the cancelled lookup made %d attempts", calls)
	}
}
