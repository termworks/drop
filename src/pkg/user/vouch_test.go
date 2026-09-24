package user

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// aMachine is a machine of its own: a config directory, the key drop makes there, and its badge.
func aMachine(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SSH_AUTH_SOCK", "")
	Use("")
	SignWith("")
	if _, _, err := Mine(time.Now()); err != nil {
		t.Fatalf("Mine(): %v", err)
	}
}

// somebody is a user key held somewhere else.
func somebody(t *testing.T) ssh.Signer {
	t.Helper()
	_, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	by, err := ssh.NewSignerFromKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	return by
}

func same(a, b ssh.PublicKey) bool { return bytes.Equal(a.Marshal(), b.Marshal()) }

func TestAVouchedBadgeIsWornByTheMachineItNames(t *testing.T) {
	aMachine(t)
	owner := somebody(t)
	now := time.Now()

	signed, sig, err := Sign(owner, deviceID(), "phone", now)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := Pack(signed, sig)
	if err != nil {
		t.Fatal(err)
	}
	badge, got, err := Unpack(packed, now)
	if err != nil {
		t.Fatalf("Unpack(): %v", err)
	}
	if !bytes.Equal(badge.Bytes(), signed.Bytes()) {
		t.Fatalf("unpacked a different badge:\n%s\nwant\n%s", badge.Bytes(), signed.Bytes())
	}
	if err := Wear(badge, got, now); err != nil {
		t.Fatalf("Wear(): %v", err)
	}

	pub, err := Public()
	if err != nil || !same(pub, owner.PublicKey()) {
		t.Fatalf("Public() = %v, %v; want the owner's key", pub, err)
	}
	worn, _, err := Mine(now)
	if err != nil || !same(worn.User, owner.PublicKey()) {
		t.Fatalf("Mine() = %v, %v; want the vouched badge", worn.User, err)
	}
	if _, quiet := Quiet(); quiet {
		t.Fatal("a machine holding only the public half signs quietly")
	}
	where, _ := Where()
	if _, err := os.Stat(where + ".before"); err != nil {
		t.Fatalf("the key it had was not set aside: %v", err)
	}
}

func TestACodeShownForAnotherMachineIsRefused(t *testing.T) {
	aMachine(t)
	owner := somebody(t)
	now := time.Now()

	signed, sig, err := Sign(owner, "0000000000000000000000000000000000000000000000000000000000000000", "laptop", now)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := Pack(signed, sig)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Unpack(packed, now); err == nil {
		t.Fatal("a badge for another machine was taken as this one's")
	}
}

func TestAVouchedMachineWearsARunOutBadgeRatherThanNone(t *testing.T) {
	aMachine(t)
	owner := somebody(t)
	now := time.Now()

	signed, sig, err := Sign(owner, deviceID(), "phone", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := Wear(signed, sig, now); err != nil {
		t.Fatal(err)
	}

	worn, _, err := Mine(now.Add(Lasts + time.Hour))
	if !errors.Is(err, ErrStale) {
		t.Fatalf("Mine() after it ran out: %v, want ErrStale", err)
	}
	if !same(worn.User, owner.PublicKey()) {
		t.Fatal("a run-out badge was not worn")
	}
}

func TestARenewedBadgeReplacesOnlyAnOlderOne(t *testing.T) {
	aMachine(t)
	owner := somebody(t)
	now := time.Now()

	first, sig, err := Sign(owner, deviceID(), "phone", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := Wear(first, sig, now); err != nil {
		t.Fatal(err)
	}

	later := now.Add(40 * 24 * time.Hour)
	fresh, freshSig, err := Sign(owner, deviceID(), "phone", later)
	if err != nil {
		t.Fatal(err)
	}
	kept, _, err := Renewed(Bundle(fresh, freshSig), later)
	if err != nil || kept.Until.Unix() != fresh.Until.Unix() {
		t.Fatalf("Renewed() = %v, %v; want the fresh badge", kept.Until, err)
	}
	kept, _, err = Renewed(Bundle(first, sig), later)
	if err != nil || kept.Until.Unix() != fresh.Until.Unix() {
		t.Fatalf("an older badge replaced a newer one: %v, %v", kept.Until, err)
	}

	stranger, strangerSig, err := Sign(somebody(t), deviceID(), "phone", later)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Renewed(Bundle(stranger, strangerSig), later); err == nil {
		t.Fatal("a badge signed by somebody else was kept")
	}
}

func TestACarriedKeyMakesTheSameUser(t *testing.T) {
	aMachine(t)
	seed, err := Export()
	if err != nil {
		t.Fatalf("Export(): %v", err)
	}
	was, err := Public()
	if err != nil {
		t.Fatal(err)
	}

	aMachine(t)
	if err := Import(seed); err != nil {
		t.Fatalf("Import(): %v", err)
	}
	now, err := Public()
	if err != nil || !same(now, was) {
		t.Fatalf("Public() = %v, %v; want the carried key", now, err)
	}
	badge, _, err := Mine(time.Now())
	if err != nil || !same(badge.User, was) {
		t.Fatalf("Mine() = %v, %v; want a badge signed by the carried key", badge.User, err)
	}
	if _, quiet := Quiet(); !quiet {
		t.Fatal("a machine holding the key does not sign quietly")
	}
}

func TestAMachineThatChangesHandsTwiceLeavesToItsOwnKey(t *testing.T) {
	aMachine(t)
	own, err := Public()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, owner := range []ssh.Signer{somebody(t), somebody(t)} {
		signed, sig, err := Sign(owner, deviceID(), "phone", now)
		if err != nil {
			t.Fatal(err)
		}
		if err := Wear(signed, sig, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := Leave(); err != nil {
		t.Fatalf("Leave(): %v", err)
	}
	back, err := Public()
	if err != nil || !same(back, own) {
		t.Fatalf("left to %v, %v; want the key it started with", back, err)
	}
}
