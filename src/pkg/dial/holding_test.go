package dial

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	keys "github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

const forTesting = "drop/testing"

func idFor(seed byte) node.ID {
	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return keys.NewSecretKey(raw).Public().EndpointID()
}

func entryFor(seed byte) book.Entry {
	return book.Entry{Name: "alpha", ID: idFor(seed)}
}

// A stream failing says its own connection is finished, not whatever is held now. Another caller
// may have dialled a fresh one in between, and closing that ends a conversation to tidy up after
// somebody else's failure.
func TestDroppingAConnectionLeavesANewerOneAlone(t *testing.T) {
	held := Hold(nil, nil, nil)
	entry := entryFor(1)

	newer, older := &iroh.Conn{}, &iroh.Conn{}
	held.open[key(entry.ID, forTesting)] = newer

	held.drop(entry.ID, forTesting, older)

	if held.open[key(entry.ID, forTesting)] != newer {
		t.Fatal("dropping an old connection took away the one being held")
	}
}

// Two callers reaching for one device dial once. Without that, the second connection replaces the
// first and closes a pipe the first caller is in the middle of using.
func TestOnlyOneDialPerDeviceIsInFlight(t *testing.T) {
	held := Hold(nil, nil, nil)
	entry := entryFor(2)

	// Somebody else's dial, already going. Anything that dials here instead reaches for a node
	// that is not there.
	going := &flight{done: make(chan struct{})}
	held.dialling[key(entry.ID, forTesting)] = going

	nothingAnswered := errors.New("nothing answered")
	go func() {
		time.Sleep(10 * time.Millisecond)
		going.err = nothingAnswered
		close(going.done)
	}()

	_, err := held.dial(context.Background(), entry, forTesting)
	if !errors.Is(err, nothingAnswered) {
		t.Fatalf("a second caller came back with %v, want what the dial in flight found", err)
	}
}

// And waiting on somebody else's dial ends when the caller's own context does.
func TestWaitingOnADialEndsWithTheCaller(t *testing.T) {
	held := Hold(nil, nil, nil)
	entry := entryFor(3)

	held.dialling[key(entry.ID, forTesting)] = &flight{done: make(chan struct{})}

	ctx, stop := context.WithCancel(context.Background())
	stop()

	if _, err := held.dial(ctx, entry, forTesting); !errors.Is(err, context.Canceled) {
		t.Fatalf("a caller that gave up came back with %v", err)
	}
}
