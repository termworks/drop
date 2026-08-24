package book

import (
	"bytes"
	"crypto/rand"
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
