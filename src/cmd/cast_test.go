package cmd

import (
	"testing"

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
