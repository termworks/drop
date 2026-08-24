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
	if who.Name != "phone" {
		t.Errorf("the machine is called %q", who.Name)
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
