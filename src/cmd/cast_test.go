package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/asciicast"
	"github.com/bresilla/drop/src/pkg/ns"
)

// Access is denied unless something grants it. A cast that declares a namespace and no rule is one
// nobody can ever watch, and the only sign of it is a refusal on the watcher's screen.
func TestACastIsWatchableByAPairedDevice(t *testing.T) {
	table := castMounts(reading())

	paired := ns.Caller{ID: "beta", Name: "beta", Paired: true}
	if ok, why := table.Admits(CastPath, paired); !ok {
		t.Fatalf("a paired device may not watch a cast: %s", why)
	}

	// And no further: a cast is somebody's screen.
	stranger := ns.Caller{ID: "nobody-in-particular"}
	if ok, _ := table.Admits(CastPath, stranger); ok {
		t.Error("an unpaired device may watch a cast")
	}
}

func TestAddressCleanupOnlyRemovesItsOwnFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cast-address")
	removeFirst, err := publishAddress(path, "first")
	if err != nil {
		t.Fatal(err)
	}
	removeSecond, err := publishAddress(path, "second")
	if err != nil {
		t.Fatal(err)
	}

	removeFirst()
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "second" {
		t.Fatalf("new address = %q, %v", raw, err)
	}
	if stat, err := os.Stat(path); err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatalf("address mode = %v, %v", stat, err)
	}

	removeSecond()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("address remained after its owner left: %v", err)
	}
}

func TestCastReaderStopsDeliveringAfterCancellation(t *testing.T) {
	reader, _, err := asciicast.NewReader(strings.NewReader(
		"{\"version\":2,\"width\":80,\"height\":24}\n[0.1,\"o\",\"first\"]\n[0.2,\"o\",\"second\"]\n",
	))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := reads(ctx, reader)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()

	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-deadline.C:
			t.Fatal("cast reader remained blocked after cancellation")
		}
	}
}
