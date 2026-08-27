package among

import (
	"crypto/rand"
	"testing"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// Two people, as a user key is written down, and a third nobody here has met.
const (
	aliceKey   = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 alice\n"
	bobKey     = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE6 bob\n"
	carolKey   = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE7 carol\n"
	machineKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE8 me\n"
)

func anID(t *testing.T) node.ID {
	t.Helper()

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	return sk.Public().EndpointID()
}

func aSecret(t *testing.T) []byte {
	t.Helper()

	secret := make([]byte, book.SecretBytes)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("generating a secret: %v", err)
	}
	return secret
}

// aBook is an address book with a machine each for alice and bob, and one paired on its own.
//
// The first machine of a person is filed under their name, so that is what a rule naming them says.
func aBook(t *testing.T) *book.Book {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	b, err := book.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	b.Pair("alice", anID(t), aSecret(t))
	b.Belongs("alice", aliceKey)
	b.Pair("bob", anID(t), aSecret(t))
	b.Belongs("bob", bobKey)
	b.Pair("builder", anID(t), aSecret(t))
	return b
}

func names(entries []book.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

func has(list []string, want string) bool {
	for _, at := range list {
		if at == want {
			return true
		}
	}
	return false
}

// The rule is the membership: whoever it names holds the namespace, and nobody keeps a second list.
func TestWhoHoldsItIsWhoTheRuleNames(t *testing.T) {
	b := aBook(t)

	held := names(Holders(ns.Access{Named: []string{"alice"}}, b))
	if len(held) != 1 || held[0] != "alice" {
		t.Fatalf("Holders(alice) = %v, want her machine", held)
	}

	held = names(Holders(ns.Access{AnyPaired: true}, b))
	if len(held) != 3 {
		t.Fatalf("Holders(paired) = %v, want everybody in the book", held)
	}
}

// Widening the rule widens the set, because there is nothing else to change.
func TestChangingTheRuleChangesWhoHoldsIt(t *testing.T) {
	b := aBook(t)

	rule := ns.Access{Named: []string{"alice"}}
	if held := names(Holders(rule, b)); len(held) != 1 {
		t.Fatalf("Holders() = %v", held)
	}

	rule.Named = append(rule.Named, "bob")
	held := names(Holders(rule, b))
	if len(held) != 2 || !has(held, "alice") || !has(held, "bob") {
		t.Fatalf("Holders() = %v, want both of them", held)
	}
}

// A namespace behind a password has no set anybody can work out: nobody is admitted by a rule they
// have not answered, and there is nothing here to answer with.
func TestAPasswordNamesNobody(t *testing.T) {
	b := aBook(t)

	if held := Holders(ns.Access{Password: "$argon2id$v=19$m=65536,t=3,p=1$c2FsdA$aGFzaA"}, b); len(held) != 0 {
		t.Fatalf("Holders(a password) = %v", names(held))
	}
}

// What travels is the people, by the key they sign with, because a name in an address book is one
// machine's private label and means nothing anywhere else.
func TestPeopleAreNamedByTheKeyTheySignWith(t *testing.T) {
	b := aBook(t)

	who := People(ns.Access{Named: []string{"alice"}}, b, machineKey)
	if len(who) != 2 || !has(who, aliceKey) || !has(who, machineKey) {
		t.Fatalf("People() = %v, want alice and this machine", who)
	}
}

// Whether the person a change was signed by was allowed to make it is the access rule's question,
// asked of the same rule that decides whether their machine may open the path.
func TestAChangeIsTakenFromSomebodyTheRuleAdmits(t *testing.T) {
	b := aBook(t)
	admits := Admits(ns.Access{Named: []string{"alice"}}, b, machineKey)

	if !admits(aliceKey) {
		t.Fatal("alice is named and her change was refused")
	}
	if admits(bobKey) {
		t.Fatal("bob is not named and his change was taken")
	}
	if admits(carolKey) {
		t.Fatal("a change from somebody nobody here has met was taken")
	}
	if admits("") {
		t.Fatal("a change signed by nobody was taken")
	}
}

// This machine's own person always is. A machine that refused its owner's changes would refuse
// everything they wrote on another machine of theirs.
func TestMyOwnChangesAreAlwaysTaken(t *testing.T) {
	b := aBook(t)

	if !Admits(ns.Access{}, b, machineKey)(machineKey) {
		t.Fatal("this machine refused its own person")
	}
}

// A rule that says nothing admits nobody, which is what forgetting to write one has to mean.
func TestARuleThatSaysNothingAdmitsNobody(t *testing.T) {
	b := aBook(t)

	if held := Holders(ns.Access{}, b); len(held) != 0 {
		t.Fatalf("Holders(nothing) = %v", names(held))
	}
	if Admits(ns.Access{}, b, "")(aliceKey) {
		t.Fatal("a rule that says nothing took a change")
	}
}

// A change carries the person who signed it and not the machine they were at, so a rule that names
// one of somebody's machines is read as naming them.
//
// The two halves of membership have to give one answer: a machine the rule admits is one Holders
// names, and Holders naming it while Admits refuses everything it signs is a namespace that goes on
// dialling somebody whose changes it will never take.
func TestAChangeIsJudgedAgainstEveryMachineOfThePerson(t *testing.T) {
	b := aBook(t)
	b.Pair("laptop", anID(t), aSecret(t))
	b.Belongs("laptop", bobKey)

	rule := ns.Access{Named: []string{"bob@laptop"}}

	holders := Holders(rule, b)
	if len(holders) != 1 || holders[0].Name != "laptop" {
		t.Fatalf("Holders() = %v, want bob's laptop", names(holders))
	}
	if !Admits(rule, b, "")(bobKey) {
		t.Fatal("a change by the person whose machine the rule names was refused")
	}
}

// The same rule still refuses somebody it names no machine of.
func TestAChangeBySomebodyNoMachineOfWhoseIsNamedIsRefused(t *testing.T) {
	b := aBook(t)
	b.Pair("laptop", anID(t), aSecret(t))
	b.Belongs("laptop", bobKey)

	if Admits(ns.Access{Named: []string{"bob@laptop"}}, b, "")(aliceKey) {
		t.Fatal("a change by somebody the rule names no machine of was taken")
	}
}
