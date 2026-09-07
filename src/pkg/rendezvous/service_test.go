package rendezvous

import (
	"context"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

type relayMemory struct {
	mu      sync.Mutex
	records map[string][]byte
	changed chan struct{}
}

func newRelayMemory() *relayMemory {
	return &relayMemory{records: make(map[string][]byte), changed: make(chan struct{}, 1)}
}

func (r *relayMemory) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	key := path.Base(req.URL.Path)
	switch req.Method {
	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.records[key] = body
		r.mu.Unlock()
		select {
		case r.changed <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		r.mu.Lock()
		body, ok := r.records[key]
		body = append([]byte(nil), body...)
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (r *relayMemory) waitFor(t *testing.T, count int) {
	t.Helper()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		r.mu.Lock()
		held := len(r.records)
		r.mu.Unlock()
		if held >= count {
			return
		}
		select {
		case <-r.changed:
		case <-timer.C:
			t.Fatalf("relay holds %d records, want %d", held, count)
		}
	}
}

func TestServicePublishesResolvesAndRetires(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("DROP_PORT", "0")

	wasRendezvous, wasDirect := node.Rendezvous(), node.Direct()
	node.SetRendezvous(false)
	node.SetDirect(true)
	t.Cleanup(func() {
		node.SetRendezvous(wasRendezvous)
		node.SetDirect(wasDirect)
	})

	n, err := node.Start(t.Context())
	if err != nil {
		t.Fatalf("starting node: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })
	want := netip.MustParseAddrPort("192.0.2.44:47777")
	n.Endpoint.AddExternalAddr(want)

	shared := secret(3)
	b, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	b.Pair("peer", id(9), shared)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	relay := newRelayMemory()
	server := httptest.NewServer(relay)
	t.Cleanup(server.Close)

	service, err := New(n, server.URL)
	if err != nil {
		t.Fatalf("creating service: %v", err)
	}
	t.Cleanup(service.closeAll)

	if err := service.publishRound(time.Now()); err != nil {
		t.Fatalf("publishing: %v", err)
	}
	relay.waitFor(t, len(PublishEpochs(time.Now())))

	service.mu.Lock()
	publishers := len(service.publishers)
	service.mu.Unlock()
	if publishers != 2 {
		t.Fatalf("service holds %d publishers, want 2", publishers)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	got, found := service.Find(ctx, book.Entry{ID: n.ID(), Secret: shared})
	if !found {
		t.Fatal("published node was not resolved")
	}
	if got.ID != n.ID() {
		t.Fatalf("resolved identity is %s, want %s", got.ID, n.ID())
	}
	if direct := Direct(got); !hasAddr(direct, want) {
		t.Fatalf("resolved addresses are %v, want %s", direct, want)
	}

	b, err = book.Load()
	if err != nil {
		t.Fatal(err)
	}
	b.Remove("peer")
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if err := service.publishRound(time.Now()); err != nil {
		t.Fatalf("retiring: %v", err)
	}

	service.mu.Lock()
	publishers = len(service.publishers)
	service.mu.Unlock()
	if publishers != 0 {
		t.Fatalf("service retained %d publishers after unpairing", publishers)
	}
}

func TestNewRefusesAMalformedRelayURL(t *testing.T) {
	if _, err := New(nil, "://not-a-relay"); err == nil {
		t.Fatal("New() accepted a malformed relay URL")
	}
}

func hasAddr(all []netip.AddrPort, want netip.AddrPort) bool {
	for _, one := range all {
		if one == want {
			return true
		}
	}
	return false
}

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
