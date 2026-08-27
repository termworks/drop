package cmd

import (
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/meet"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// What a peer says about a namespace it offers, as a joiner reads it off the hello.
func offering(archetype string, shared ns.Shared) proto.Served {
	return proto.Served{Path: "/notes", Archetype: archetype, Version: 1, Shared: shared}
}

// aliceMade is a namespace alice made at /notes, as every machine holding it works the name out.
var aliceMade = ns.Shared{Creator: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 alice\n", At: "/notes", Nonce: "cafe"}

// A namespace of a kind one machine holds alone is nobody else's to hold, however the far end
// describes it. The kind is this machine's own question: joining one would put a terminal up here
// under a rule naming whoever offered it.
func TestJoiningAKindOneMachineHoldsAloneIsRefused(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	serves := []proto.Served{offering("tty", aliceMade)}
	address := ns.Address{Machine: "bob", Path: "/notes"}

	_, err := joinable(reading(), serves, address)
	if err == nil {
		t.Fatal("a tty offered as shared was accepted for joining")
	}
	if !strings.Contains(err.Error(), "one machine's own") {
		t.Fatalf("joinable() = %v", err)
	}
}

// One that several machines may hold is still joinable.
func TestJoiningAKindSeveralMachinesHoldIsAllowed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	serves := []proto.Served{offering("chat", aliceMade)}
	address := ns.Address{Machine: "bob", Path: "/notes"}

	if _, err := joinable(reading(), serves, address); err != nil {
		t.Fatalf("joinable() = %v", err)
	}
}

// A machine that says it made a namespace is naming the path it made it at, and that is a path it
// serves. Saying otherwise is a name worked out from nothing the far end holds.
func TestANamespaceTheServerClaimsToHaveMadeSomewhereElseIsRefused(t *testing.T) {
	entry := book.Entry{Name: "bob", Person: "bob", User: aliceMade.Creator}
	served := proto.Served{Path: "/list", Archetype: "chat", Shared: aliceMade}

	err := offered(served, entry)
	if err == nil {
		t.Fatal("a peer serving its own namespace under a different name was believed")
	}
	if !strings.Contains(err.Error(), "/list") || !strings.Contains(err.Error(), "/notes") {
		t.Fatalf("offered() = %v, want both paths named", err)
	}
}

// Relaying somebody else's namespace is ordinary: the peer did not make it and says so.
func TestANamespaceMadeBySomebodyElseIsBelieved(t *testing.T) {
	entry := book.Entry{Name: "bob", Person: "bob", User: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE6 bob\n"}
	served := proto.Served{Path: "/list", Archetype: "chat", Shared: aliceMade}

	if err := offered(served, entry); err != nil {
		t.Fatalf("offered() = %v", err)
	}
}

// The name a namespace goes by is the far end's word, and this is the part of it a joiner can check
// for itself: a namespace already held here is one thing, and one thing is held once.
//
// Two paths carrying one name are two access rules over one history. What arrives through either is
// written into the other, and what the other sends goes out under a rule its holders never agreed
// to.
func TestANamespaceAlreadyHeldHereIsNotTakenUpTwice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	store, err := made.Load()
	if err != nil {
		t.Fatalf("made.Load(): %v", err)
	}
	if err := store.Add("/notes", made.Entry{Archetype: "chat", Shared: aliceMade}); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	line := made.Line{Path: "/bobs-list", Entry: made.Entry{Archetype: "chat", Shared: aliceMade}}
	at, twice := already(nil, store, line)
	if !twice {
		t.Fatal("a namespace already held here was taken up again under a second path")
	}
	if at != "/notes" {
		t.Fatalf("already() = %q, want the path it is held at", at)
	}
}

// The one written in the config counts too.
func TestANamespaceTheConfigHoldsIsNotTakenUpTwice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	store, err := made.Load()
	if err != nil {
		t.Fatalf("made.Load(): %v", err)
	}

	mounts := []ns.Mount{{Path: "/notes", Archetype: "chat", Shared: aliceMade}}
	line := made.Line{Path: "/bobs-list", Entry: made.Entry{Archetype: "chat", Shared: aliceMade}}

	if at, twice := already(mounts, store, line); !twice || at != "/notes" {
		t.Fatalf("already() = %q, %v, want /notes", at, twice)
	}
}

// A different namespace at a different path is a different namespace.
func TestAnotherNamespaceIsNotMistakenForOneAlreadyHeld(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	store, err := made.Load()
	if err != nil {
		t.Fatalf("made.Load(): %v", err)
	}
	if err := store.Add("/notes", made.Entry{Archetype: "chat", Shared: aliceMade}); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	other := ns.Shared{Creator: aliceMade.Creator, At: "/diary", Nonce: "beef"}
	line := made.Line{Path: "/diary", Entry: made.Entry{Archetype: "chat", Shared: other}}

	if at, twice := already(nil, store, line); twice {
		t.Fatalf("already() = %q, want a different namespace to be its own", at)
	}
}

// Joining names the person joined from and nobody else, so anybody else holding it is somebody
// whose changes are passed over — along with everything made after one of theirs. Saying so at join
// time is the difference between a namespace that stops moving and one that looks caught up.
func TestJoiningNamesTheHoldersWhoseChangesWillBeRefused(t *testing.T) {
	key := asSomebody(t)
	pinned, _ := bookWith(t, key)
	pinned.Pair("carol", idFor(7), make([]byte, book.SecretBytes))
	pinned.Belongs("carol", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE7 carol\n")
	if err := pinned.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	holders := []string{key, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE7 carol\n", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE9 dave\n"}

	who := notNamed(holders, ns.Access{Named: []string{"bob"}})
	if len(who.named) != 1 || who.named[0] != "carol" {
		t.Fatalf("notNamed() named %v, want carol", who.named)
	}
	if who.strangers != 1 {
		t.Fatalf("notNamed() counted %d holders nobody here has paired with, want 1", who.strangers)
	}

	said := strings.Join(sayUnheard("/notes", who), "\n")
	if !strings.Contains(said, "carol") {
		t.Fatalf("nothing was said about carol: %q", said)
	}
	if !strings.Contains(said, "drop path grant /notes carol") {
		t.Fatalf("nothing said what to do about carol: %q", said)
	}
	if !strings.Contains(said, "not paired with") {
		t.Fatalf("nothing was said about the holder nobody here has met: %q", said)
	}
}

// The person joined from holds it, and so does this machine's own person. Neither is news.
func TestJoiningSaysNothingAboutHoldersItAlreadyTakesChangesFrom(t *testing.T) {
	key := asSomebody(t)
	pinned, entry := bookWith(t, key)
	if err := pinned.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	who := notNamed([]string{key}, ns.Access{Named: []string{personOf(entry)}})
	if len(who.named) != 0 || who.strangers != 0 {
		t.Fatalf("notNamed() = %+v, want nobody", who)
	}
	if said := sayUnheard("/notes", who); len(said) != 0 {
		t.Fatalf("something was said about nobody: %v", said)
	}
}

// A count of what came over and no count of what did not is a namespace that reads as caught up and
// is not: a refused change takes everything made after it with it.
func TestWhatWasRefusedIsSaidBesideWhatCameOver(t *testing.T) {
	said := howMuch(meet.Caught{Taken: 1, Refused: 2})
	if !strings.Contains(said, "1 change came over") {
		t.Fatalf("howMuch() = %q", said)
	}
	if !strings.Contains(said, "2 were refused") {
		t.Fatalf("howMuch() = %q, want the refusals said too", said)
	}

	if said := howMuch(meet.Caught{Refused: 1}); said != "nothing came over, and 1 was refused" {
		t.Fatalf("howMuch() = %q", said)
	}
	if said := howMuch(meet.Caught{}); said != "nothing has happened there yet" {
		t.Fatalf("howMuch() = %q", said)
	}
	if said := howMuch(meet.Caught{Taken: 3}); said != "3 changes came over" {
		t.Fatalf("howMuch() = %q", said)
	}
}
