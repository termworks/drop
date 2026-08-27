package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bresilla/drop/src/pkg/metal"
	"github.com/tmc/go-iroh/key"
)

// A machine with nothing written down is named by itself, and named the same way next time.
//
// This is the whole point of taking the name off the hardware: wiping the disk leaves a machine
// with nothing written down, and it has to come back as the machine it was.
func TestAMachineWithNothingWrittenDownComesBackAsItself(t *testing.T) {
	if !metal.Read().Held() {
		t.Skip("this machine says nothing about itself, so there is nothing to derive from")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	was, err := LocalID()
	if err != nil {
		t.Fatalf("LocalID(): %v", err)
	}

	// Nothing was written down, so there is nothing a backup could carry and nothing a wipe could
	// take. A fresh config directory is exactly what a reinstall leaves behind.
	path, err := Written()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s was written even though the machine names itself", path)
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now, err := LocalID()
	if err != nil {
		t.Fatalf("LocalID() after a wipe: %v", err)
	}
	if now != was {
		t.Fatalf("after a wipe this machine is %s, and it was %s", Brief(now), Brief(was))
	}

	// And it says what it was named by, so a person can see what would change it.
	mark, err := Naming()
	if err != nil {
		t.Fatal(err)
	}
	if !mark.Held() || mark.Says == "" {
		t.Fatalf("the machine named itself but will not say from what: %+v", mark)
	}
}

// A machine that has been running with a key of its own keeps it. Deriving a different one on an
// ordinary upgrade would break every pairing that names this machine, without anybody asking.
func TestAKeyAlreadyWrittenDownStillWins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	made, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	seed := made.Bytes()

	at := filepath.Join(dir, "drop")
	if err := os.MkdirAll(at, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(at, "identity"), seed[:], 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LocalID()
	if err != nil {
		t.Fatalf("LocalID(): %v", err)
	}
	if want := made.Public().EndpointID(); got != want {
		t.Fatalf("this machine is %s, and its own key says %s", Brief(got), Brief(want))
	}

	// And it does not claim the hardware named it, because the hardware did not.
	mark, err := Naming()
	if err != nil {
		t.Fatal(err)
	}
	if mark.Held() {
		t.Fatalf("a machine using a written-down key says it was named by %s", mark.Says)
	}
}
