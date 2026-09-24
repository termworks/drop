package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/shares"
)

func pairedPerson(t *testing.T) (alice, desktop book.Entry) {
	t.Helper()

	alice = book.Entry{Name: "alice", ID: idFor(61)}
	desktop = book.Entry{Name: "desktop", ID: idFor(62)}
	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	pinned.Pair(alice.Name, alice.ID, pairSecret(11))
	pinned.Belongs(alice.Name, aliceKey)
	pinned.Pair(desktop.Name, desktop.ID, pairSecret(12))
	pinned.Belongs(desktop.Name, aliceKey)
	if err := pinned.Save(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []book.Entry{alice, desktop} {
		if err := shares.Remember(entry.ID, []proto.Served{{Path: "/chat", Archetype: "chat"}}); err != nil {
			t.Fatal(err)
		}
	}
	return alice, desktop
}

func TestManagingAndForgettingAPersonUsesEveryMachine(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	alice, desktop := pairedPerson(t)
	back := &running{held: dial.Hold(nil, nil, nil)}

	managed, err := back.Managed("alice")
	if err != nil {
		t.Fatal(err)
	}
	if managed.ID != "" || managed.Machines != 2 {
		t.Fatalf("managed person = %+v", managed)
	}
	if err := back.Forget("alice"); err != nil {
		t.Fatal(err)
	}

	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned.All()) != 0 {
		t.Fatalf("forgetting alice left %+v", pinned.All())
	}
	for _, entry := range []book.Entry{alice, desktop} {
		if got, err := shares.Recall(entry.ID); err != nil || got != nil {
			t.Fatalf("cached shares for %s = %+v, %v", entry.Name, got, err)
		}
	}
}

func TestCommandForgetRemovesCachedShares(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	alice, _ := pairedPerson(t)
	cmd := newPeerForgetCmd()

	if err := cmd.RunE(cmd, []string{alice.Name}); err != nil {
		t.Fatal(err)
	}
	if got, err := shares.Recall(alice.ID); err != nil || got != nil {
		t.Fatalf("cached shares after command forget = %+v, %v", got, err)
	}
}

func TestCacheCleanupFailureKeepsThePairing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	alice, _ := pairedPerson(t)
	base, err := convo.DataDir()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "shares", alice.ID.String()+".json")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(file, "held"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := forgetKnown(alice.Name, false); err == nil {
		t.Fatal("forget succeeded without removing cached shares")
	}
	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pinned.Lookup(alice.Name); !ok {
		t.Fatal("cache cleanup failure removed the pairing")
	}
}

func TestAmbiguousPersonStateCannotBeManaged(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	alice, _ := pairedPerson(t)

	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := pinned.Change(func() (bool, error) {
		pinned.Remove(alice.Name)
		pinned.Pair(alice.Name, idFor(63), pairSecret(16))
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}

	back := &running{held: dial.Hold(nil, nil, nil)}
	if _, err := back.Managed(alice.Name); err == nil {
		t.Fatal("managed a name shared by a person and a machine")
	}
	if err := back.Trust(alice.Name, true); err == nil {
		t.Fatal("trusted a name shared by a person and a machine")
	}
	if err := back.Forget(alice.Name); err == nil {
		t.Fatal("forgot a name shared by a person and a machine")
	}

	after, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.All()) != 2 {
		t.Fatalf("ambiguous operations changed the address book: %+v", after.All())
	}
}
