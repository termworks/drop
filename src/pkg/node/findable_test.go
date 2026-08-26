package node

import (
	"os"
	"path/filepath"
	"testing"
)

// A device told not to tell a relay it exists must not write a record to one when a pairing code is
// shown. The record goes up under this device's own endpoint id, so it says that id is alive and
// what address the machine writing it came from — and with the rendezvous off it carries no relay,
// so nobody holding the ticket can dial anything out of it either.
func TestNothingIsPublishedWithTheRendezvousOff(t *testing.T) {
	n := started(t)

	// Publishing starts by reading this device's key. A key that will not read is how a test tells
	// a device that published nothing from one that published under a key it went and got.
	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity"), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Findable(t.Context(), n); err != nil {
		t.Fatalf("a device with the rendezvous off went for its key to publish: %v", err)
	}

	// And with it on, the same call does go looking — otherwise the check above proves nothing.
	SetRendezvous(true)
	if err := Findable(t.Context(), n); err == nil {
		t.Fatal("a device with the rendezvous on published without reading its key")
	}
}
