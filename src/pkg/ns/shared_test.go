package ns

import "testing"

// Two people, as a user key is written down.
const (
	aliceKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 alice\n"
	bobKey   = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE6 bob\n"
)

// The name is worked out rather than issued, so every machine given the same three facts arrives at
// it without anybody being asked.
func TestTheSameThreeFactsAreTheSameName(t *testing.T) {
	one := Shared{Creator: aliceKey, At: "/notes", Nonce: "cafe"}
	two := Shared{Creator: aliceKey, At: "/notes", Nonce: "cafe"}

	if one.ID() != two.ID() {
		t.Fatalf("%s and %s", one.ID(), two.ID())
	}
	if len(one.ID()) != 64 {
		t.Fatalf("ID() = %q, want something a directory can be called", one.ID())
	}
}

// A machine has to be able to hold two different things at one path over time without them being
// confused for each other, so the word is part of the name.
func TestTwoThingsAtOnePathAreTwoNames(t *testing.T) {
	was := Shared{Creator: aliceKey, At: "/notes", Nonce: "first"}
	now := Shared{Creator: aliceKey, At: "/notes", Nonce: "second"}

	if was.ID() == now.ID() {
		t.Fatalf("both are called %s", was.ID())
	}
}

// Who made it is part of the name too: two people who each declare /notes have declared two things.
func TestTwoPeopleAtOnePathAreTwoNames(t *testing.T) {
	hers := Shared{Creator: aliceKey, At: "/notes", Nonce: "cafe"}
	his := Shared{Creator: bobKey, At: "/notes", Nonce: "cafe"}

	if hers.ID() == his.ID() {
		t.Fatalf("both are called %s", hers.ID())
	}
}

// One that names nobody is a namespace this machine holds alone, and has no name at all.
func TestOneNobodyMadeIsNotShared(t *testing.T) {
	var none Shared

	if none.Declared() {
		t.Fatal("an undeclared shared namespace says it is one")
	}
	if none.ID() != "" {
		t.Fatalf("ID() = %q", none.ID())
	}
}

// A mount carries it, because identity belongs to the namespace and not to what is served there.
func TestATableKeepsWhatANamespaceIsCalled(t *testing.T) {
	shared := Shared{Creator: aliceKey, At: "/notes", Nonce: "cafe"}

	table := NewTable()
	if err := table.Add(Mount{Path: "/notes", Archetype: "chat", Access: Access{AnyPaired: true}, Shared: shared}); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	m, _, ok := table.Lookup("/notes")
	if !ok {
		t.Fatal("nothing is mounted at /notes")
	}
	if m.Shared.ID() != shared.ID() {
		t.Fatalf("the mount calls it %s, want %s", m.Shared.ID(), shared.ID())
	}
}
