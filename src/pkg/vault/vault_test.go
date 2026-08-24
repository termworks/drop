package vault

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNothingConfiguredIsNotAFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	v, err := Open(nil)
	if err != nil {
		t.Fatalf("Open(nil): %v", err)
	}
	if v.On() {
		t.Error("a node with no vault came back encrypting")
	}
	if len(v.Key()) != 0 {
		t.Error("a node with no vault has a data key")
	}
}

// The data key is made once and unwrapped every time after: a key that changed on restart would
// take the history with it.
func TestTheDataKeySurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	at := []string{filepath.Join(dir, "vault.key")}

	first, err := Open(at)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if !first.On() {
		t.Fatal("the vault is not encrypting")
	}

	again, err := Open(at)
	if err != nil {
		t.Fatalf("Open() a second time: %v", err)
	}
	if string(again.Key()) != string(first.Key()) {
		t.Error("the data key changed between starts")
	}
}

// A key file is what age writes: readable, one key, 0600.
func TestAKeyFileIsWrittenWhereTheConfigSaid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	at := filepath.Join(dir, "keys", "vault.key")
	if _, err := Open([]string{at}); err != nil {
		t.Fatal(err)
	}

	stat, err := os.Stat(at)
	if err != nil {
		t.Fatalf("no key file: %v", err)
	}
	if mode := stat.Mode().Perm(); mode != 0o600 {
		t.Errorf("the key file is %o", mode)
	}
}

// With the key gone the device is locked, which is a different thing from having no vault: the
// history is there and unreadable, and saying so is the only honest answer.
func TestAVaultWithoutItsKeyIsLocked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	at := filepath.Join(dir, "vault.key")
	if _, err := Open([]string{at}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(at); err != nil {
		t.Fatal(err)
	}

	_, err := Open([]string{at})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("a vault with no key came back with %v, wanted a locked device", err)
	}
}

// A recipient is a public key: the vault wraps to it and cannot open it again on its own, which is
// exactly what naming hardware in a config means.
func TestARecipientCanBeWrappedToWithoutBeingHeld(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	held := filepath.Join(dir, "vault.key")
	first, err := Open([]string{held})
	if err != nil {
		t.Fatal(err)
	}

	// The public half of that key, as somebody would paste it into a config.
	identity, err := keyFile(held, false)
	if err != nil {
		t.Fatal(err)
	}

	// A fresh vault wrapped to both: the file, and the recipient on its own.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	both, err := Open([]string{held, identity.Recipient().String()})
	if err != nil {
		t.Fatalf("wrapping to a recipient: %v", err)
	}
	if !both.On() {
		t.Error("a vault wrapped to a recipient is not encrypting")
	}
	if string(both.Key()) == string(first.Key()) {
		t.Error("two vaults share a data key")
	}
}
