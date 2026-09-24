package node

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// One place, wiped and used again — which is what a reinstall leaves behind. Not two places:
	// two places are two drops, and they are supposed to differ.
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)

	was, err := LocalID()
	if err != nil {
		t.Fatalf("LocalID(): %v", err)
	}

	// The secret remains derived while its public identity is pinned.
	path, err := Written()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s was written even though the machine names itself", path)
	}
	anchored, exists, err := readHardware(path + ".hardware")
	if err != nil || !exists || anchored != was {
		t.Fatalf("hardware anchor = %s, %t, %v", Brief(anchored), exists, err)
	}

	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
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

func testSecret(t *testing.T) key.SecretKey {
	t.Helper()
	secret, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestHardwareIdentityIsPinned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity")
	first := testSecret(t)
	hardware := &hardwareIdentity{secret: first}

	selected, _, err := chooseIdentity(path, hardware)
	if err != nil {
		t.Fatalf("chooseIdentity(): %v", err)
	}
	if selected.Public().EndpointID() != first.Public().EndpointID() {
		t.Fatal("the selected identity differs from the hardware identity")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("hardware identity was written as a secret: %v", err)
	}
	anchored, exists, err := readHardware(path + ".hardware")
	if err != nil || !exists || anchored != first.Public().EndpointID() {
		t.Fatalf("hardware anchor = %s, %t, %v", Brief(anchored), exists, err)
	}

	if _, _, err := chooseIdentity(path, hardware); err != nil {
		t.Fatalf("choosing the pinned identity again: %v", err)
	}
	other := &hardwareIdentity{secret: testSecret(t)}
	if _, _, err := chooseIdentity(path, other); err == nil || !strings.Contains(err.Error(), "refusing to change identity") {
		t.Fatalf("changed hardware identity = %v", err)
	}
}

func TestMissingHardwareDoesNotReplaceAPinnedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity")
	hardware := &hardwareIdentity{secret: testSecret(t)}
	if _, _, err := chooseIdentity(path, hardware); err != nil {
		t.Fatal(err)
	}

	if _, _, err := chooseIdentity(path, nil); err == nil || !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("missing hardware = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a replacement identity was written: %v", err)
	}
}

func TestCorruptHardwareAnchorIsRefused(t *testing.T) {
	for name, raw := range map[string][]byte{
		"short":   []byte("broken"),
		"invalid": []byte(strings.Repeat("z", hardwareIdentitySize)),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "identity")
			if err := os.WriteFile(path+".hardware", raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := chooseIdentity(path, &hardwareIdentity{secret: testSecret(t)}); err == nil {
				t.Fatal("corrupt hardware anchor was accepted")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("an identity was written after corruption: %v", err)
			}
		})
	}
}

func TestWrittenIdentityWinsOverHardwareState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity")
	written := testSecret(t)
	seed := written.Bytes()
	if err := os.WriteFile(path, seed[:], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".hardware", []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	selected, from, err := chooseIdentity(path, &hardwareIdentity{secret: testSecret(t)})
	if err != nil {
		t.Fatalf("chooseIdentity(): %v", err)
	}
	if selected.Public().EndpointID() != written.Public().EndpointID() || from.Held() {
		t.Fatal("hardware state displaced the written identity")
	}
}

func TestMixedConcurrentFirstStartsCannotSplitIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity")
	first := &hardwareIdentity{secret: testSecret(t)}
	second := &hardwareIdentity{secret: testSecret(t)}
	choices := []*hardwareIdentity{nil, first, second}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ids []ID
	for i := 0; i < 24; i++ {
		choice := choices[i%len(choices)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			selected, _, err := chooseIdentity(path, choice)
			if err != nil {
				return
			}
			mu.Lock()
			ids = append(ids, selected.Public().EndpointID())
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if len(ids) == 0 {
		t.Fatal("every concurrent identity selection failed")
	}
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("concurrent starts selected %s and %s", Brief(ids[0]), Brief(id))
		}
	}
	_, stored, storedErr := readIdentity(path)
	_, anchored, anchorErr := readHardware(path + ".hardware")
	if storedErr != nil || anchorErr != nil {
		t.Fatalf("stored state = %v, anchor state = %v", storedErr, anchorErr)
	}
	if stored == anchored {
		t.Fatalf("stored identity present = %t, hardware anchor present = %t", stored, anchored)
	}
}

func TestRebindPinsHardwareBeforeRetiringStoredIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity")
	old := testSecret(t)
	seed := old.Bytes()
	if err := os.WriteFile(path, seed[:], 0o600); err != nil {
		t.Fatal(err)
	}
	current := hardwareIdentity{secret: testSecret(t)}

	was, kept, err := rebindHardware(path, current)
	if err != nil {
		t.Fatalf("rebindHardware(): %v", err)
	}
	if was != old.Public().EndpointID() || kept != path+".was" {
		t.Fatalf("rebind returned %s, %s", Brief(was), kept)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stored identity remains: %v", err)
	}
	retired, exists, err := readIdentity(kept)
	if err != nil || !exists || retired.Public().EndpointID() != was {
		t.Fatalf("retired identity = %s, %t, %v", Brief(retired.Public().EndpointID()), exists, err)
	}
	anchored, exists, err := readHardware(path + ".hardware")
	if err != nil || !exists || anchored != current.secret.Public().EndpointID() {
		t.Fatalf("hardware anchor = %s, %t, %v", Brief(anchored), exists, err)
	}
}

func TestRebindPreservesAnExistingRecoveryIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity")
	old := testSecret(t)
	seed := old.Bytes()
	if err := os.WriteFile(path, seed[:], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".was", []byte("recovery"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := rebindHardware(path, hardwareIdentity{secret: testSecret(t)}); err == nil || !strings.Contains(err.Error(), "recovery identity") {
		t.Fatalf("rebind with recovery identity = %v", err)
	}
	if raw, err := os.ReadFile(path + ".was"); err != nil || string(raw) != "recovery" {
		t.Fatalf("recovery identity = %q, %v", raw, err)
	}
	stored, exists, err := readIdentity(path)
	if err != nil || !exists || stored.Public().EndpointID() != old.Public().EndpointID() {
		t.Fatalf("stored identity changed: %t, %v", exists, err)
	}
	if _, err := os.Stat(path + ".hardware"); !os.IsNotExist(err) {
		t.Fatalf("hardware anchor was changed before refusal: %v", err)
	}
}

// Two drops on one machine are two drops. An account of their own, a profile, or a test bringing
// up a second node — each keeps its things somewhere else, and each has to be reachable as itself.
// Two of them on one address is not one machine with two people on it, it is nobody.
func TestTwoDropsOnOneMachineAreNotOneAddress(t *testing.T) {
	if !metal.Read().Held() {
		t.Skip("this machine says nothing about itself, so there is nothing to derive from")
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	one, err := LocalID()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	two, err := LocalID()
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatalf("two drops on one machine both answer to %s", Brief(one))
	}

	// A profile is the same thing: its own place, so its own name.
	t.Setenv("DROP_PROFILE", "bob")
	three, err := LocalID()
	if err != nil {
		t.Fatal(err)
	}
	if three == two || three == one {
		t.Fatalf("a profile answers to the same address as the drop it runs beside: %s", Brief(three))
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

// A machine has one identity, however many drops start at once.
//
// `drop serve` and `drop peer pair` are separate processes over one config directory, so two of
// them reaching a machine with nothing written down is the ordinary way to start. If they each made
// a key, the one whose write landed second would own the file while the other ran its whole session
// signing as a key that is not there — and every pairing a peer recorded in that session would name
// an address that is gone at the next restart, silently.
func TestAMachineGetsOneIdentityHoweverManyDropsStartAtOnce(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// On a machine that names itself this holds because they all derive the same key and nothing is
	// written at all; on one that does not, it holds because the first to reach the file makes the
	// key and the rest are handed what it made. Both are the same promise, so both are asserted.
	if metal.Read().Held() {
		t.Log("this machine names itself, so what is asserted here is the derivation, not the lock")
	}

	var wg sync.WaitGroup
	got := make([]ID, 8)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := LocalID()
			if err != nil {
				t.Errorf("LocalID(): %v", err)
				return
			}
			got[i] = id
		}()
	}
	wg.Wait()

	// Every one of them, and the file on the disk, are the same machine.
	for i, id := range got {
		if id != got[0] {
			t.Fatalf("drop %d is %s and drop 0 is %s", i, Brief(id), Brief(got[0]))
		}
	}

	again, err := LocalID()
	if err != nil {
		t.Fatal(err)
	}
	if again != got[0] {
		t.Fatalf("what is on the disk is %s and what was handed out was %s", Brief(again), Brief(got[0]))
	}
}
