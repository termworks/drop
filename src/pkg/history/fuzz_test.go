package history

import (
	"testing"
)

// A change arrives from whoever else holds the same namespace, is stored, and is replayed every
// time the thing is read back. Whatever a peer sends, unpack must answer rather than panic, and
// what it hands back must be inside the limits that make replaying a history bounded work.
func FuzzUnpackChange(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})
	f.Add(make([]byte, 200))
	f.Add([]byte("drop-change/1"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		c, err := unpack(raw)
		if err != nil {
			return
		}

		switch {
		case len(c.Heads) > MaxHeads:
			t.Fatalf("a change came back naming %d changes, over the %d limit", len(c.Heads), MaxHeads)
		case len(c.Body) > MaxBody:
			t.Fatalf("a change came back with a %d byte body, over the %d limit", len(c.Body), MaxBody)
		case len(c.Fold) > MaxHeld:
			t.Fatalf("a fold came back covering %d changes, over the %d limit", len(c.Fold), MaxHeld)
		}

		// Nothing may come back bigger than the bytes it came from.
		if len(c.Body) > len(raw) || len(c.Heads)*len(ID{}) > len(raw) {
			t.Fatalf("%d bytes unpacked a %d byte body and %d heads", len(raw), len(c.Body), len(c.Heads))
		}

		// A change must not name itself: it is filed under a digest of its own bytes, and one that
		// is its own head is a history that can never be ordered.
		id := c.ID()
		for _, h := range c.Heads {
			if h == id {
				t.Fatal("a change came back naming itself as what came before it")
			}
		}
	})
}
