package rendezvous

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
)

func TestProbePeer(t *testing.T) {
	want := os.Getenv("PROBE_PEER")
	if want == "" {
		t.Skip("set PROBE_PEER")
	}

	b, _ := book.Load()
	entry, ok := b.Lookup(want)
	if !ok {
		t.Fatalf("%s not in the book", want)
	}

	s, err := New(nil, Relay)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 6; i++ {
		ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		addr, found := s.Find(ctx, entry)
		stop()

		if !found {
			t.Logf("%2ds  not publishing", i*5)
		} else {
			t.Logf("%2ds  %v", i*5, addr.Addrs())
		}
		time.Sleep(5 * time.Second)
	}
}
