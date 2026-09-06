package rendezvous

import (
	"context"
	"iter"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

func TestPreviousEpochCanAnswerWhileCurrentIsSlow(t *testing.T) {
	now := time.Unix(100*int64(Epoch/time.Second)+1, 0)
	entry := book.Entry{ID: id(7), Secret: secret(1)}
	epochs := ResolveEpochs(now)
	current, err := Derive(entry.Secret, entry.ID, epochs[0])
	if err != nil {
		t.Fatal(err)
	}
	previous, err := Derive(entry.Secret, entry.ID, epochs[1])
	if err != nil {
		t.Fatal(err)
	}

	want := netip.MustParseAddrPort("192.0.2.7:47777")
	resolver := iroh.AddressResolverFunc(func(ctx context.Context, at node.ID) iter.Seq2[iroh.Item, error] {
		return func(yield func(iroh.Item, error) bool) {
			if at == current.Public().EndpointID() {
				<-ctx.Done()
				return
			}
			if at != previous.Public().EndpointID() {
				return
			}
			resolved := netaddr.NewEndpointAddr(at, netaddr.IPAddr{Addr: want})
			yield(iroh.NewItem(dns.EndpointInfoFromAddr(resolved), "test", nil), nil)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	service := &Service{resolver: resolver}
	got, found := service.findAt(ctx, entry, now)
	if !found {
		t.Fatal("the previous epoch did not answer while the current lookup was blocked")
	}
	if direct := Direct(got); len(direct) != 1 || direct[0] != want {
		t.Fatalf("resolved %v, want %v", direct, want)
	}
}

func TestUnreadableBookStopsStalePublishers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "drop"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drop", "peers.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	sk, err := Derive(secret(1), id(7), EpochAt(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := iroh.NewPkarrPublisher(sk, "http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = publisher.Close() })

	service := &Service{publishers: map[string]*iroh.PkarrPublisher{"stale": publisher}}
	err = service.publishRound(time.Now())
	if err == nil || !strings.Contains(err.Error(), "address book") {
		t.Fatalf("publishRound() returned %v, want an address book error", err)
	}
	if len(service.publishers) != 0 {
		t.Fatal("the stale publisher is still running")
	}
}
