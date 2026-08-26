package cmd

import (
	"testing"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

const bobsKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJu+hpbPjgfQgbHcYNoWOhLrULYBUR8ie4AX837IdXrv\n"

func emptyBook(t *testing.T) *book.Book {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	pinned, err := book.Load()
	if err != nil {
		t.Fatalf("book.Load(): %v", err)
	}
	return pinned
}

// The machine itself is in the book. This is how drop worked before people existed and has to keep
// working exactly as it did.
func TestAPairedMachineIsKnownWithoutABadge(t *testing.T) {
	pinned := emptyBook(t)
	id := idFor(1)
	pinned.Pair("laptop", id, make([]byte, book.SecretBytes))

	who := whoIs(pinned)(id, proto.Badged{})
	if who.Name != "laptop" || !who.Paired {
		t.Fatalf("a paired machine came out as %+v", who)
	}
	if who.UserName != "" {
		t.Error("a machine with no badge was given a person")
	}
}

// A machine nobody has paired with, carrying a badge from somebody who is in the book. This is the
// point of the whole thing: pair once per person, not once per pair of machines.
func TestAnUnknownMachineOfAKnownPersonIsRecognised(t *testing.T) {
	pinned := emptyBook(t)
	pinned.Pair("bob", idFor(1), make([]byte, book.SecretBytes))
	pinned.Belongs("bob", bobsKey)

	who := whoIs(pinned)(idFor(2), proto.Badged{Key: bobsKey, As: "phone"})
	if who.UserName != "bob" {
		t.Fatalf("bob's phone came out as %+v", who)
	}
	if !who.Paired {
		t.Error("bob's phone was not treated as paired")
	}
	// It has no local name: nobody here has filed it under anything. What it calls itself is kept
	// for a list to show, and nothing is decided on it.
	if who.Name != "" {
		t.Errorf("a machine nobody filed was named %q", who.Name)
	}
	if who.Label != "phone" {
		t.Errorf("the machine calls itself %q", who.Label)
	}
}

// The narrow form of a rule names a machine this side wrote down. A machine that merely calls
// itself that must not satisfy it, or the half of the rule that narrows it does nothing: anybody
// holding bob's badge could set the name and walk in.
func TestAMachineCannotNameItselfIntoANarrowRule(t *testing.T) {
	pinned := emptyBook(t)
	pinned.Pair("bob", idFor(1), make([]byte, book.SecretBytes))
	pinned.Belongs("bob", bobsKey)

	rule := ns.Access{Named: []string{"bob@laptop"}}

	claiming := whoIs(pinned)(idFor(2), proto.Badged{Key: bobsKey, As: "laptop"})
	if ok, _ := rule.Admits(claiming); ok {
		t.Fatal("a machine got in by calling itself laptop")
	}

	// The same person's machine, filed here under that name, is the one the rule meant.
	pinned.Pair("laptop", idFor(3), make([]byte, book.SecretBytes))
	pinned.Belongs("laptop", bobsKey)

	filed := whoIs(pinned)(idFor(3), proto.Badged{Key: bobsKey, As: "whatever"})
	if ok, why := rule.Admits(filed); !ok {
		t.Fatalf("bob's laptop was refused: %s", why)
	}
}

// A badge from somebody nobody has met is not a way in. It is read, because reading it costs
// nothing and it is signed, but it names nobody this machine knows.
func TestABadgeFromAStrangerNamesNobody(t *testing.T) {
	pinned := emptyBook(t)

	who := whoIs(pinned)(idFor(2), proto.Badged{Key: bobsKey, As: "phone"})
	if who.UserName != "" || who.Paired {
		t.Fatalf("a stranger came out as %+v", who)
	}
	if who.User != bobsKey {
		t.Error("the user key was not carried through")
	}
}

// The machine has its own entry and a badge as well. The local name wins: it is what somebody here
// wrote down, and rules on this machine are written against it.
func TestALocalNameOutranksTheBadgesName(t *testing.T) {
	pinned := emptyBook(t)
	id := idFor(3)
	pinned.Pair("buildbox", id, make([]byte, book.SecretBytes))
	pinned.Pair("bob", idFor(1), make([]byte, book.SecretBytes))
	pinned.Belongs("bob", bobsKey)

	who := whoIs(pinned)(id, proto.Badged{Key: bobsKey, As: "workstation"})
	if who.Name != "buildbox" || who.UserName != "bob" {
		t.Fatalf("came out as %+v", who)
	}
}

// My own machines answer to "me". There is nothing to pair with there — a machine is mine because
// my own user key signed its badge, and that is the whole test.
func TestMyOwnMachinesAreMe(t *testing.T) {
	pinned := emptyBook(t)

	mine.Lock()
	mine.key = bobsKey
	mine.Unlock()
	t.Cleanup(func() {
		mine.Lock()
		mine.key = ""
		mine.Unlock()
	})

	who := whoIs(pinned)(idFor(4), proto.Badged{Key: bobsKey, As: "laptop"})
	if who.UserName != "me" || !who.Paired {
		t.Fatalf("my own laptop came out as %+v", who)
	}
	if ok, why := (ns.Access{Named: []string{"me"}}).Admits(who); !ok {
		t.Errorf("a rule naming me refused my own machine: %s", why)
	}
}
