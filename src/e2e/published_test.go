//go:build cross

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/rendezvous"
)

// What a peer actually finds when it looks this device up.
//
// A device advertises addresses to itself long before any of them reach a record: a pkarr publisher
// keeps only the relay unless it is handed a filter, so reading the log a daemon prints says
// nothing at all about what was published. This asks the way a paired peer asks — through the
// rendezvous, under the identity only the two of them can compute.
func TestWhatIsPublishedIsWhatIsFound(t *testing.T) {
	entry, err := book.Resolve(far())
	if err != nil {
		t.Skipf("%s is not paired with this machine: %v", far(), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	n, err := node.Start(ctx)
	if err != nil {
		t.Fatalf("starting a node: %v", err)
	}
	defer n.Close()

	where, err := rendezvous.New(n, "")
	if err != nil {
		t.Fatalf("making a rendezvous: %v", err)
	}

	at, ok := where.Find(ctx, entry)
	if !ok {
		t.Skipf("%s has not published a rendezvous record yet", far())
	}

	relays, direct := 0, 0
	for _, one := range at.Addrs() {
		switch a := one.(type) {
		case netaddr.RelayAddr:
			relays++
			t.Logf("  relay  %s", a.URL)
		case netaddr.IPAddr:
			direct++
			t.Logf("  ip     %s", a.Addr)
		}
	}

	if relays == 0 {
		t.Error("nothing published a relay, so a device on another network cannot be reached at all")
	}
	if direct == 0 {
		t.Error("no direct address was published, so two machines on one wire still meet through a relay")
	}
}
