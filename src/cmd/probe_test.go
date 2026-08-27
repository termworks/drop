package cmd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// What does the far end actually answer a hello with?
//
// The frame carries the name and the version before the namespace list, so both can be read even
// when the list itself will not parse — which is the difference between "it is running an old
// build" and "it said nothing at all".
func TestProbeHello(t *testing.T) {
	want := os.Getenv("PROBE_PEER")
	if want == "" {
		t.Skip("set PROBE_PEER")
	}

	entry, err := book.Resolve(want)
	if err != nil {
		t.Fatal(err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()

	n, err := node.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()

	lan, _ := discovery.StartLAN(ctx, n)

	done, s, err := best(n, lan).To(ctx, entry, node.ALPNHello)
	if err != nil {
		t.Fatalf("could not reach it: %v", err)
	}
	defer done.Close()
	defer s.Close()

	conn := wire.NewConn(s)
	if err := conn.WriteFrame(wire.KindPing, nil); err != nil {
		t.Fatal(err)
	}

	_, body, err := conn.ReadFrame()
	if err != nil {
		t.Fatalf(">>> it closed without answering: %v", err)
	}
	t.Logf(">>> answered with %d bytes", len(body))

	r := wire.NewReader(body)
	name, err := r.String(wire.MaxString)
	if err != nil {
		t.Fatalf(">>> no name in the answer: %v", err)
	}
	version, err := r.String(wire.MaxString)
	if err != nil {
		t.Fatalf(">>> no version in the answer: %v", err)
	}
	t.Logf(">>> name=%q version=%q", name, version)

	count, err := r.Uint()
	if err != nil {
		t.Fatalf(">>> no namespace count: %v", err)
	}
	t.Logf(">>> it claims %d namespaces", count)

	for i := uint64(0); i < count; i++ {
		path, err := r.String(wire.MaxString)
		if err != nil {
			t.Fatalf(">>> entry %d has no path: %v", i, err)
		}
		kind, err := r.Byte()
		if err != nil {
			t.Fatalf(">>> entry %d has no kind: %v", i, err)
		}
		writable, err := r.Bool()
		if err != nil {
			t.Fatalf(">>> entry %d has no writable: %v", i, err)
		}
		locked, err := r.Bool()
		if err != nil {
			t.Fatalf(">>> entry %d (%s) STOPS AFTER writable — an older build: %v", i, path, err)
		}
		t.Logf("    %s kind=%d writable=%v locked=%v", path, kind, writable, locked)
	}
	t.Logf(">>> the whole answer parsed: this build matches")
}
