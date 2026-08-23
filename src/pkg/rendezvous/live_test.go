package rendezvous

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/node"
)

// TestLiveRoundTrip publishes a record to a real pkarr relay and reads it back.
//
// Off unless DROP_LIVE is set. It reaches a relay this project does not own, so it is not part of
// the ordinary gate: a test suite should not start talking to a third party because someone typed
// `go test`.
func TestLiveRoundTrip(t *testing.T) {
	if os.Getenv("DROP_LIVE") == "" {
		t.Skip("set DROP_LIVE=1 to run this; it publishes to a public pkarr relay")
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	node.SetRendezvous(true)
	t.Cleanup(func() { node.SetRendezvous(false) })

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	n, err := node.Start(ctx)
	if err != nil {
		t.Fatalf("starting the node: %v", err)
	}
	defer n.Close()

	// A relay address is what gets published, and there is none until the endpoint has reached one.
	if err := n.Endpoint.Online(ctx); err != nil {
		t.Fatalf("reaching a relay: %v", err)
	}

	addr := n.Endpoint.Addr()
	t.Logf("this node advertises %d address(es)", len(addr.Addrs()))

	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}

	epoch := EpochAt(time.Now())
	sk, err := Derive(secret, n.ID(), epoch)
	if err != nil {
		t.Fatalf("deriving: %v", err)
	}
	at := sk.Public().EndpointID()
	t.Logf("publishing under the derived identity %s", at.Short())

	if at == n.ID() {
		t.Fatal("the record is keyed by this node's real identity")
	}

	publisher, err := iroh.NewPkarrPublisher(sk, Relay, nil)
	if err != nil {
		t.Fatalf("the publisher: %v", err)
	}
	defer publisher.Close()

	publisher.Publish(dns.EndpointDataFromAddr(addr))

	resolver, err := iroh.NewPkarrResolver(Relay, nil)
	if err != nil {
		t.Fatalf("the resolver: %v", err)
	}

	// Publishing is fire-and-forget, so the record shows up shortly after rather than at once.
	deadline := time.Now().Add(60 * time.Second)
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		for item, err := range resolver.Resolve(ctx, at) {
			if err != nil {
				t.Logf("attempt %d: %v", attempt, err)
				continue
			}
			got := item.EndpointInfo()
			if got.ID != at {
				t.Fatalf("the record came back under %s, not %s", got.ID.Short(), at.Short())
			}
			if len(got.Addr().Addrs()) == 0 {
				t.Logf("attempt %d: the record is there but carries no address", attempt)
				continue
			}

			t.Logf("resolved %d address(es) from the relay", len(got.Addr().Addrs()))
			for _, a := range got.Addr().Addrs() {
				t.Logf("  %s", a)
			}
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatal("the record never came back from the relay")
}
