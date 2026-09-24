package cmd

import (
	stdbytes "bytes"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/proto"
)

const (
	aliceKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 alice"
	carolKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 carol"
)

func pairSecret(fill byte) []byte {
	return stdbytes.Repeat([]byte{fill}, book.SecretBytes)
}

func trustPairing(t *testing.T, name string) {
	t.Helper()

	b, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Change(func() (bool, error) {
		b.Trust(name, true)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedPairingKeepsTrustForTheSameOwner(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	peer := idFor(41)

	first := proto.Pairing{Peer: peer, Secret: pairSecret(1), User: aliceKey, Addrs: []string{"192.0.2.1:47777"}}
	if _, err := filed(first, "laptop", false); err != nil {
		t.Fatal(err)
	}
	trustPairing(t, "laptop")

	nextSecret := pairSecret(2)
	second := proto.Pairing{Peer: peer, Secret: nextSecret, User: aliceKey, Addrs: []string{"192.0.2.2:47777"}}
	if _, err := filed(second, "laptop", false); err != nil {
		t.Fatal(err)
	}

	b, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := b.Lookup("laptop")
	if !ok {
		t.Fatal("the repeated pairing disappeared")
	}
	if !entry.Trusted || entry.User != aliceKey {
		t.Fatalf("the repeated pairing lost its identity decision: %+v", entry)
	}
	if !stdbytes.Equal(entry.Secret, nextSecret) || len(entry.Addrs) != 1 || entry.Addrs[0] != "192.0.2.2:47777" {
		t.Fatalf("the repeated pairing kept stale connection state: %+v", entry)
	}
}

func TestOneEndpointCannotBeFiledUnderTwoNames(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	peer := idFor(42)
	original := pairSecret(3)

	if _, err := filed(proto.Pairing{Peer: peer, Secret: original}, "laptop", true); err != nil {
		t.Fatal(err)
	}
	_, err := filed(proto.Pairing{Peer: peer, Secret: pairSecret(4)}, "desktop", true)
	if err == nil || !strings.Contains(err.Error(), "already filed as \"laptop\"") {
		t.Fatalf("filing one endpoint twice returned %v", err)
	}

	b, loadErr := book.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(b.All()) != 1 {
		t.Fatalf("the refused pairing left %d address-book entries", len(b.All()))
	}
	entry, ok := b.Lookup("laptop")
	if !ok || !stdbytes.Equal(entry.Secret, original) {
		t.Fatalf("the refused pairing changed the original entry: %+v", entry)
	}
	if _, ok := b.Lookup("desktop"); ok {
		t.Fatal("the second name was written despite the refusal")
	}
}

func TestChangedPairingOwnerDoesNotInheritTrust(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	peer := idFor(43)

	if _, err := filed(proto.Pairing{Peer: peer, Secret: pairSecret(5), User: aliceKey}, "laptop", false); err != nil {
		t.Fatal(err)
	}
	trustPairing(t, "laptop")
	if _, err := filed(proto.Pairing{Peer: peer, Secret: pairSecret(6), User: carolKey}, "laptop", false); err != nil {
		t.Fatal(err)
	}

	b, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := b.Lookup("laptop")
	if !ok {
		t.Fatal("the repeated pairing disappeared")
	}
	if entry.User != carolKey || entry.Trusted {
		t.Fatalf("trust crossed from the previous owner: %+v", entry)
	}
}

func TestPairingCannotReuseAPersonName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if _, err := filed(proto.Pairing{Peer: idFor(44), Secret: pairSecret(7), User: aliceKey}, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := filed(proto.Pairing{Peer: idFor(45), Secret: pairSecret(8), User: aliceKey}, "desktop", false); err != nil {
		t.Fatal(err)
	}
	if err := forgetKnown("alice", false); err != nil {
		t.Fatal(err)
	}

	_, err := filed(proto.Pairing{Peer: idFor(46), Secret: pairSecret(9), User: carolKey}, "alice", false)
	if err == nil || !strings.Contains(err.Error(), "already names a person") {
		t.Fatalf("reusing alice for another person returned %v", err)
	}

	pinned, loadErr := book.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, ok := pinned.Lookup("alice"); ok {
		t.Fatal("the colliding pairing was written")
	}
	if desktop, ok := pinned.Lookup("desktop"); !ok || desktop.Person != "alice" || desktop.User != aliceKey {
		t.Fatalf("the original person changed: %+v, %v", desktop, ok)
	}
}

func TestStandaloneMachineCannotReuseAPersonName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if _, err := filed(proto.Pairing{Peer: idFor(47), Secret: pairSecret(10), User: aliceKey}, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := filed(proto.Pairing{Peer: idFor(48), Secret: pairSecret(11), User: aliceKey}, "desktop", false); err != nil {
		t.Fatal(err)
	}
	if err := forgetKnown("alice", false); err != nil {
		t.Fatal(err)
	}

	_, err := filed(proto.Pairing{Peer: idFor(49), Secret: pairSecret(12)}, "alice", true)
	if err == nil || !strings.Contains(err.Error(), "already names a person") {
		t.Fatalf("reusing alice for a standalone machine returned %v", err)
	}
}

func TestPersonCanReuseTheirOwnName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if _, err := filed(proto.Pairing{Peer: idFor(50), Secret: pairSecret(13), User: aliceKey}, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := filed(proto.Pairing{Peer: idFor(51), Secret: pairSecret(14), User: aliceKey}, "desktop", false); err != nil {
		t.Fatal(err)
	}
	if err := forgetKnown("alice", false); err != nil {
		t.Fatal(err)
	}

	if _, err := filed(proto.Pairing{Peer: idFor(52), Secret: pairSecret(15), User: aliceKey}, "alice", false); err != nil {
		t.Fatalf("reusing alice for the same person: %v", err)
	}
}
