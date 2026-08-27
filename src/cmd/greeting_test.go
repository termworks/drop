package cmd

import (
	"testing"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

func idFor(seed byte) node.ID {
	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

// served builds a table where one path is shared with bob and one with nobody.
func served(t *testing.T) *ns.Table {
	t.Helper()

	table := ns.NewTable()
	for _, m := range []ns.Mount{
		{Path: "/shared", Archetype: "share", Access: ns.Access{Named: []string{"laptop"}}},
		{Path: "/private", Archetype: "tty"},
	} {
		if err := table.Add(m); err != nil {
			t.Fatalf("adding %s: %v", m.Path, err)
		}
	}
	return table
}

// What a device serves says a great deal about it — that it has a terminal, what it files where.
// Hello is answered by anyone who dials, so the list has to be withheld from a stranger.
func TestAStrangerLearnsNothingAboutNamespaces(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	pinned, err := book.Load()
	if err != nil {
		t.Fatalf("book.Load(): %v", err)
	}

	hello := greeting(pinned, served(t), reading(), idFor(9), proto.Badged{})
	if len(hello.Serves) != 0 {
		t.Fatalf("an unpaired caller was told about %+v", hello.Serves)
	}
	if hello.Name == "" {
		t.Fatal("the name should still be answered")
	}
}

// Being merely known is not enough either: pinning a device records its id, which is not consent.
func TestAKnownButUnpairedDeviceLearnsNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	pinned, err := book.Load()
	if err != nil {
		t.Fatalf("book.Load(): %v", err)
	}
	id := idFor(4)
	pinned.Pin("seen", id)

	if hello := greeting(pinned, served(t), reading(), id, proto.Badged{}); len(hello.Serves) != 0 {
		t.Fatalf("a pinned but unpaired caller was told about %+v", hello.Serves)
	}
}

// Pairing is no longer a key to everything. A device is told about the paths its own name appears
// on, and nothing else — which is what makes "chat with bob, but he never sees the terminal" real.
func TestAPairedDeviceIsToldOnlyItsOwnPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	pinned, err := book.Load()
	if err != nil {
		t.Fatalf("book.Load(): %v", err)
	}
	id := idFor(5)
	pinned.Pair("laptop", id, make([]byte, book.SecretBytes))

	hello := greeting(pinned, served(t), reading(), id, proto.Badged{})

	shown := map[string]bool{}
	for _, s := range hello.Serves {
		shown[s.Path] = true
	}

	if !shown["/shared"] {
		t.Fatalf("the path naming this device was withheld: %+v", hello.Serves)
	}
	if shown["/private"] {
		t.Fatal("a path with no rule was shown to a paired device")
	}
}

// A namespace anybody at all may hold admits a stranger, and who else holds it is for the people
// inside. A key is the same on every machine, so naming the holders to whoever dialled hands out
// the public identity of everybody in the book.
func TestAStrangerIsNotToldWhoHoldsAPublicNamespace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	pinned, err := book.Load()
	if err != nil {
		t.Fatalf("book.Load(): %v", err)
	}
	pinned.Pair("alice", idFor(1), make([]byte, book.SecretBytes))
	pinned.Belongs("alice", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 alice\n")

	table := ns.NewTable()
	if err := table.Add(ns.Mount{
		Path:      "/public",
		Archetype: "chat",
		Access:    ns.Access{Anyone: true},
		Shared:    ns.Shared{Creator: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 alice\n", At: "/public"},
	}); err != nil {
		t.Fatalf("adding /public: %v", err)
	}

	hello := greeting(pinned, table, reading(), idFor(9), proto.Badged{})
	if len(hello.Serves) != 1 {
		t.Fatalf("a namespace anybody may hold was withheld: %+v", hello.Serves)
	}
	if len(hello.Serves[0].Holders) != 0 {
		t.Fatalf("a stranger was told who holds it: %v", hello.Serves[0].Holders)
	}
}

// Somebody already met is told, because that is what joining takes.
func TestAPairedCallerIsToldWhoHoldsIt(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	pinned, err := book.Load()
	if err != nil {
		t.Fatalf("book.Load(): %v", err)
	}
	id := idFor(5)
	pinned.Pair("laptop", id, make([]byte, book.SecretBytes))
	pinned.Belongs("laptop", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE6 bob\n")

	table := ns.NewTable()
	if err := table.Add(ns.Mount{
		Path:      "/notes",
		Archetype: "chat",
		Access:    ns.Access{Named: []string{"laptop"}},
		Shared:    ns.Shared{Creator: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE6 bob\n", At: "/notes"},
	}); err != nil {
		t.Fatalf("adding /notes: %v", err)
	}

	hello := greeting(pinned, table, reading(), id, proto.Badged{})
	if len(hello.Serves) != 1 || len(hello.Serves[0].Holders) == 0 {
		t.Fatalf("a paired caller was told nothing about who holds it: %+v", hello.Serves)
	}
}
