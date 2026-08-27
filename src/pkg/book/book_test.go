package book

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
)

func testID(t *testing.T) node.ID {
	t.Helper()

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	return sk.Public().EndpointID()
}

func testSecret(t *testing.T) []byte {
	t.Helper()

	secret := make([]byte, SecretBytes)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("generating secret: %v", err)
	}
	return secret
}

// The secret is what makes a peer privately findable, so it has to survive a round trip intact.
func TestPairSurvivesReload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	id, secret := testID(t), testSecret(t)

	b, err := Load()
	if err != nil {
		t.Fatalf("Load() on empty config: %v", err)
	}
	b.Pair("laptop", id, secret)
	if err := b.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after Save(): %v", err)
	}
	entry, ok := reloaded.Lookup("laptop")
	if !ok {
		t.Fatal("laptop is missing after reload")
	}
	if entry.ID != id {
		t.Fatalf("id = %s, want %s", entry.ID, id)
	}
	if !bytes.Equal(entry.Secret, secret) {
		t.Fatal("the pairing secret did not survive the round trip")
	}
	if !entry.Paired() {
		t.Fatal("Paired() is false for an entry with a secret")
	}
}

// A pinned peer has a name but no secret, and must not claim to be paired.
func TestPinIsNotPaired(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	id := testID(t)

	b, _ := Load()
	b.Pin("desktop", id)
	if err := b.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	reloaded, _ := Load()
	entry, ok := reloaded.Lookup("desktop")
	if !ok {
		t.Fatal("desktop is missing after reload")
	}
	if entry.Paired() {
		t.Fatal("a pinned entry reports itself as paired")
	}
	if len(reloaded.Paired()) != 0 {
		t.Fatal("Paired() listed an entry that has no secret")
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	id, secret := testID(t), testSecret(t)

	b, _ := Load()
	b.Pair("laptop", id, secret)
	if err := b.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	byName, err := Resolve("laptop")
	if err != nil {
		t.Fatalf("Resolve(name): %v", err)
	}
	if byName.ID != id || !byName.Paired() {
		t.Fatalf("Resolve(name) = %+v", byName)
	}

	// A bare peer id that is already known must come back with its secret, not as a stranger.
	byID, err := Resolve(id.String())
	if err != nil {
		t.Fatalf("Resolve(peer id): %v", err)
	}
	if byID.ID != id || !byID.Paired() {
		t.Fatal("Resolve(peer id) lost the pairing")
	}

	if _, err := Resolve("nothing-like-this"); err == nil {
		t.Fatal("Resolve() accepted a name that is neither known nor a peer id")
	}
}

func TestRemove(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	b, _ := Load()
	b.Pair("laptop", testID(t), testSecret(t))

	if !b.Remove("laptop") {
		t.Fatal("Remove() reported a miss for a known name")
	}
	if b.Remove("laptop") {
		t.Fatal("Remove() reported a hit for an unknown name")
	}
	if len(b.All()) != 0 {
		t.Fatal("book is not empty after removing its only entry")
	}
}

// A daemon holds the address book in memory while `drop pair` writes to it from another process.
// Without noticing that, a device paired while the daemon was up stays a stranger to it, which
// looks exactly like pairing being broken.
func TestAPairingByAnotherProcessIsNoticed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	beta := testID(t)

	serving, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	// Somebody else pairs, and saves.
	pairing, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	pairing.Pair("beta", beta, testSecret(t))
	if err := pairing.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	if _, ok := serving.ByID(beta); ok {
		t.Fatal("the running book knew about a pairing it had not read")
	}

	if err := serving.Refresh(); err != nil {
		t.Fatalf("Refresh(): %v", err)
	}
	if _, ok := serving.ByID(beta); !ok {
		t.Fatal("a pairing made by another process was still not seen after a refresh")
	}
}

// Finding a device is the expensive part of talking to it. The address that answered is the best
// guess for next time, and it is only worth anything if it is written down.
func TestTheAddressThatAnsweredIsRemembered(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	beta := testID(t)

	b, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	b.Pair("beta", beta, testSecret(t), "10.8.0.2:47777", "192.168.1.9:47777")
	if err := b.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	changed, err := b.Reached(beta, "192.168.1.9:47777")
	if err != nil || !changed {
		t.Fatalf("Reached() = %v, %v", changed, err)
	}

	// First, and nothing lost: the others may work from somewhere else tomorrow.
	entry, _ := b.Lookup("beta")
	if len(entry.Addrs) != 2 || entry.Addrs[0] != "192.168.1.9:47777" {
		t.Fatalf("addrs = %v, want the one that answered first", entry.Addrs)
	}

	// And it survives being read back by another process.
	again, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if entry, _ := again.Lookup("beta"); entry.Addrs[0] != "192.168.1.9:47777" {
		t.Fatalf("after reloading, addrs = %v", entry.Addrs)
	}
}

// Reaching a device at the address already at the front changes nothing, and must not rewrite the
// file for every message somebody sends.
func TestReachingTheSameAddressChangesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	beta := testID(t)

	b, _ := Load()
	b.Pair("beta", beta, testSecret(t), "192.168.1.9:47777")
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	changed, err := b.Reached(beta, "192.168.1.9:47777")
	if err != nil {
		t.Fatalf("Reached(): %v", err)
	}
	if changed {
		t.Error("it rewrote the address book to say what it already said")
	}
}

// A device nobody has paired with has nothing to remember.
func TestReachingAStrangerRemembersNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	b, _ := Load()

	if changed, err := b.Reached(testID(t), "192.168.1.9:47777"); changed || err != nil {
		t.Errorf("Reached() = %v, %v for a device that is not in the book", changed, err)
	}
}

// A person has a name of their own, and every machine of theirs carries it -- otherwise which of
// their machines was paired with first would decide what the rest of them are called.
func TestEveryMachineOfOnePersonSharesTheirName(t *testing.T) {
	const key = "ssh-ed25519 AAAA…"

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	b, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	b.Pair("bob", testID(t), testSecret(t))
	b.Belongs("bob", key)

	b.Pair("the-thing-under-his-desk", testID(t), testSecret(t))
	b.Belongs("the-thing-under-his-desk", key)

	for _, name := range []string{"bob", "the-thing-under-his-desk"} {
		entry, ok := b.Lookup(name)
		if !ok {
			t.Fatalf("%s is not in the book", name)
		}
		if entry.Person != "bob" {
			t.Errorf("%s belongs to %q", name, entry.Person)
		}
		if !entry.Owned() {
			t.Errorf("%s has no owner", name)
		}
	}
}

// Trust is a property of a person, not of one of their laptops: deciding you trust bob and then
// saying it again for each machine he owns is a decision nobody would keep up with.
func TestTrustingSomebodyTrustsEveryMachineOfTheirs(t *testing.T) {
	const key = "ssh-ed25519 AAAA…"

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b.Pair("bob", testID(t), testSecret(t))
	b.Belongs("bob", key)
	b.Pair("bobs-phone", testID(t), testSecret(t))
	b.Belongs("bobs-phone", key)
	b.Pair("carol", testID(t), testSecret(t))

	// Nobody is trusted by pairing alone.
	for _, name := range []string{"bob", "bobs-phone", "carol"} {
		if entry, _ := b.Lookup(name); entry.Trusted {
			t.Errorf("%s was trusted merely by being paired with", name)
		}
	}

	b.Trust("bob", true)

	for _, name := range []string{"bob", "bobs-phone"} {
		if entry, _ := b.Lookup(name); !entry.Trusted {
			t.Errorf("%s was not trusted along with the rest of bob's machines", name)
		}
	}
	if entry, _ := b.Lookup("carol"); entry.Trusted {
		t.Error("trusting bob trusted somebody else")
	}

	// And it comes back off the same way.
	b.Trust("bobs-phone", false)
	if entry, _ := b.Lookup("bob"); entry.Trusted {
		t.Error("withdrawing trust left one of his machines trusted")
	}
}

// One person, two machines, and one of them trusted: the answer has to be the same every time it
// is asked. A map walk returned whichever machine it reached first, so the same caller was let
// through on one connection and turned away on the next.
func TestOnePersonAnswersTheSameEveryTime(t *testing.T) {
	const key = "ssh-ed25519 AAAA…"

	write(t, fmt.Sprintf(`{
	  "laptop":  {"id": %q, "user": %q, "person": "bob", "trusted": true},
	  "desktop": {"id": %q, "user": %q, "person": "bob"}
	}`, testID(t), key, testID(t), key))

	b, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	first, ok := b.ByUser(key)
	if !ok {
		t.Fatal("ByUser() did not find a person who is in the book twice")
	}
	if !first.Trusted {
		t.Fatal("ByUser() reported bob untrusted although a machine of his is trusted")
	}
	for i := 0; i < 500; i++ {
		again, ok := b.ByUser(key)
		if !ok || again.Name != first.Name || again.Trusted != first.Trusted {
			t.Fatalf("ByUser() = %+v on try %d, %+v on the first", again, i, first)
		}
	}
}

// Trust follows the person, so a machine paired after the decision arrives with it. Otherwise the
// book says two different things about one person.
func TestAMachinePairedAfterTrustInheritsIt(t *testing.T) {
	const key = "ssh-ed25519 AAAA…"

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b.Pair("bob", testID(t), testSecret(t))
	b.Belongs("bob", key)
	b.Trust("bob", true)

	b.Pair("bobs-desktop", testID(t), testSecret(t))
	b.Belongs("bobs-desktop", key)

	if entry, _ := b.Lookup("bobs-desktop"); !entry.Trusted {
		t.Fatal("a second machine of somebody already trusted was filed as untrusted")
	}
}

// The pairing secrets in peers.json were derived once and are kept nowhere else, so the file is
// replaced whole rather than truncated and written over: an interruption must leave the old book.
func TestSaveReplacesTheBookRatherThanTruncatingIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b.Pair("laptop", testID(t), testSecret(t))
	if err := b.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	file, err := path()
	if err != nil {
		t.Fatal(err)
	}
	was, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	b.Pair("desktop", testID(t), testSecret(t))
	if err := b.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	now, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(was, now) {
		t.Fatal("Save() wrote over peers.json in place; a crash would leave no pairings at all")
	}

	// And nothing is left lying beside it.
	found, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name() != filepath.Base(file) {
		names := make([]string, 0, len(found))
		for _, at := range found {
			names = append(names, at.Name())
		}
		t.Fatalf("the config directory holds %v, want only %s", names, filepath.Base(file))
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after Save(): %v", err)
	}
	if len(reloaded.All()) != 2 {
		t.Fatalf("the reloaded book has %d entries, want 2", len(reloaded.All()))
	}
}

// A machine somebody replaced is the same machine to everyone who knew it. Pointing the entry at
// the new one has to keep everything that made it worth having: the name, the shared secret, whose
// it is, and whether it is trusted. Losing any of those is being made to pair again.
func TestAMovedMachineKeepsEverythingButWhereItWas(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	b, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	was, now := testID(t), testID(t)
	secret := testSecret(t)
	b.Pair("orin", was, secret, "1.2.3.4:47777")
	b.Belongs("orin", "ssh-ed25519 AAAA bob")
	b.Trust("orin", true)

	name, ok := b.Moved(was, now)
	if !ok || name != "orin" {
		t.Fatalf("Moved() said %q, %v", name, ok)
	}

	entry, held := b.Lookup("orin")
	if !held {
		t.Fatal("the entry went when it moved")
	}
	if entry.ID != now {
		t.Fatalf("the entry points at %v, want %v", entry.ID, now)
	}
	if !entry.Paired() {
		t.Fatal("the shared secret was lost, so the two would have to pair again")
	}
	if string(entry.Secret) != string(secret) {
		t.Fatal("the shared secret changed")
	}
	if !entry.Trusted || entry.User == "" {
		t.Fatalf("who it belongs to or whether it is trusted was lost: %+v", entry)
	}
	// Where it was is the one thing that must go: it is somewhere else now.
	if len(entry.Addrs) != 0 {
		t.Fatalf("the old addresses were kept: %v", entry.Addrs)
	}

	// And it is findable by the new id and not the old.
	if _, found := b.ByID(now); !found {
		t.Fatal("the moved machine is not findable by its new id")
	}
	if _, found := b.ByID(was); found {
		t.Fatal("the moved machine is still findable by its old id")
	}
}

// Moving is refused where it would do harm or nothing: onto itself, onto an id already in the book,
// or from a machine nobody here has heard of.
func TestAMoveThatWouldMergeOrInventAnEntryIsRefused(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	b, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	one, two, three := testID(t), testID(t), testID(t)
	b.Pin("orin", one)
	b.Pin("tron", two)

	if _, ok := b.Moved(one, one); ok {
		t.Error("a machine moved to itself")
	}
	if _, ok := b.Moved(one, two); ok {
		t.Error("a machine moved onto another machine already in the book, making two entries one")
	}
	if _, ok := b.Moved(three, testID(t)); ok {
		t.Error("a machine nobody here has heard of moved something")
	}
	if _, ok := b.Moved(node.ID{}, two); ok {
		t.Error("a move from nothing was taken")
	}

	// And nothing was disturbed by any of that.
	if entry, _ := b.Lookup("orin"); entry.ID != one {
		t.Fatalf("orin is %v, want %v", entry.ID, one)
	}
	if entry, _ := b.Lookup("tron"); entry.ID != two {
		t.Fatalf("tron is %v, want %v", entry.ID, two)
	}
}
