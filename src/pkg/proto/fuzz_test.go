package proto

import (
	"testing"
	"unicode/utf8"

	"github.com/bresilla/drop/src/pkg/plain"
)

// The two frames a stranger can put in front of this machine before anybody has been authenticated.
//
// An opening and a hello are read from a caller nobody has decided anything about yet: the path is
// resolved and the rule is checked afterwards, so whatever these decoders do, they do it for
// anyone who can reach the port. A panic here is the daemon gone; an allocation that scales with a
// number the caller sent is the machine gone.

func FuzzDecodeOpen(f *testing.F) {
	f.Add(Opening{Path: "/notes", Archetype: "note", Version: 1}.encode())
	f.Add(Opening{Meet: true, Held: "abcdef", Badge: []byte("x"), Signed: []byte("y")}.encode())
	f.Add(Opening{Plate: []byte("drop-plate/1\n"), Stamped: make([]byte, 64)}.encode())
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, body []byte) {
		open, err := decodeOpen(body)
		if err != nil {
			return
		}

		// Nothing that came back may be bigger than what it came from, and nothing may be over the
		// bound its own field was read with.
		if len(open.Path) > len(body) || len(open.Secret) > len(body) {
			t.Fatalf("a %d byte opening decoded a %d byte path and a %d byte secret",
				len(body), len(open.Path), len(open.Secret))
		}
		if len(open.Plate) > MaxSigned || len(open.Moved) > MaxSigned {
			t.Fatalf("a signed field came back over the %d byte bound", MaxSigned)
		}
		if len(open.Stamped) > MaxSignature || len(open.Handed) > MaxSignature {
			t.Fatalf("a signature came back over the %d byte bound", MaxSignature)
		}
		if open.Version > MaxVersion {
			t.Fatalf("version %d is over the %d limit", open.Version, MaxVersion)
		}
	})
}

func FuzzDecodeHello(f *testing.F) {
	f.Add(Hello{Name: "orin", Version: "0.3.2"}.encode())
	f.Add(Hello{Name: "x", Serves: []Served{{Path: "/a", Archetype: "chat", About: "a chat"}}}.encode())
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, body []byte) {
		said, err := decodeHello(body)
		if err != nil {
			return
		}

		if len(said.Serves) > MaxServed {
			t.Fatalf("%d namespaces came back, over the %d limit", len(said.Serves), MaxServed)
		}

		// Everything a hello says about itself is printed, so everything it says has to have come
		// through the cleaning. Anything that has not is a place somebody can write on a terminal.
		for _, s := range append([]string{said.Name, said.Version}, spoken(said)...) {
			if !utf8.ValidString(s) {
				t.Fatalf("%q came back and is not text", s)
			}
			if got := plain.Text(s, plain.Most); got != s && len(s) <= plain.Most {
				t.Fatalf("%q reached a listing without being made safe (would be %q)", s, got)
			}
		}
	})
}

// spoken is every string in a hello that a person is shown.
func spoken(said Hello) []string {
	var out []string
	for _, s := range said.Serves {
		out = append(out, s.Archetype, s.Shape, s.About)
	}
	return out
}
