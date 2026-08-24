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
