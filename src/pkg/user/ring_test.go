package user

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func ringSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// A badge made small enough for a doorbell is the same badge again, checked, and nothing else is.
func TestARingProofIsTheBadgeItCameFrom(t *testing.T) {
	now := time.Now()
	by := ringSigner(t)
	device := strings.Repeat("ab", 32)
	badge, sig, err := Sign(by, device, "desk", now)
	if err != nil {
		t.Fatal(err)
	}

	proof, err := RingProof(badge, sig)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof) > MaxProof {
		t.Fatalf("a proof of %d bytes does not leave the record room", len(proof))
	}

	got, err := RungBadge(proof, by.PublicKey(), device, now)
	if err != nil {
		t.Fatalf("the proof did not check out: %v", err)
	}
	if got.Name != "desk" || got.Device != device || !got.Until.Equal(badge.Until.Truncate(time.Second)) {
		t.Errorf("read back %+v, want the badge it came from", got)
	}

	cases := map[string]func() error{
		"another name": func() error {
			_, err := RungBadge(strings.Replace(proof, " desk", " laptop", 1), by.PublicKey(), device, now)
			return err
		},
		"another machine": func() error {
			_, err := RungBadge(proof, by.PublicKey(), strings.Repeat("cd", 32), now)
			return err
		},
		"another key": func() error {
			_, err := RungBadge(proof, ringSigner(t).PublicKey(), device, now)
			return err
		},
		"run out": func() error {
			_, err := RungBadge(proof, by.PublicKey(), device, now.Add(Lasts+time.Hour))
			return err
		},
		"garbled": func() error {
			_, err := RungBadge("12 !! - desk", by.PublicKey(), device, now)
			return err
		},
	}
	for what, check := range cases {
		if check() == nil {
			t.Errorf("a proof with %s was believed", what)
		}
	}
}

// A key in hardware rings too: its signatures carry the flags and counter the key added, and the
// proof keeps them.
func TestAHardwareKeyRings(t *testing.T) {
	key := asHardware(t)
	now := time.Now()
	device := strings.Repeat("ef", 32)
	badge, sig, err := Vouch(device, "phone", now)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := RingProof(badge, sig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RungBadge(proof, key.pub, device, now); err != nil {
		t.Fatalf("a hardware key's proof did not check out: %v", err)
	}
}
