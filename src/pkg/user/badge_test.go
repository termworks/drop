package user

import (
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

const someDevice = "9363f77d0f9f0fe96327693b08b9e43316b8c93bbaa862f3e18f376216d5b6f4"

// A badge says whose machine this is, and holds up when it is read back.
func TestABadgeSaysWhoOwnsWhat(t *testing.T) {
	by := aKey(t)
	now := time.Now()

	badge, sig, err := Sign(by, someDevice, "laptop", now)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}

	read, err := Read(badge.Bytes(), sig, now)
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}

	if read.Device != someDevice || read.Name != "laptop" {
		t.Errorf("read back %+v", read)
	}
	if string(read.User.Marshal()) != string(by.PublicKey().Marshal()) {
		t.Error("it names the wrong user")
	}
}

// The badge is checked against the key that actually signed it. Otherwise anybody could write
// somebody else's name on a badge and sign it themselves.
func TestABadgeSignedBySomebodyElseIsRefused(t *testing.T) {
	mine, theirs := aKey(t), aKey(t)
	now := time.Now()

	// A badge claiming to be from mine...
	claim := Badge{User: mine.PublicKey(), Device: someDevice, Name: "laptop", Until: now.Add(Lasts)}

	// ...signed by theirs.
	sig, err := Signature(theirs, claim.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Read(claim.Bytes(), sig, now); err == nil {
		t.Fatal("a badge signed by the wrong key was believed")
	}
}

// A machine that stopped being yours stops being yours, without anybody having to be told.
func TestABadgeRunsOut(t *testing.T) {
	by := aKey(t)
	now := time.Now()

	badge, sig, err := Sign(by, someDevice, "laptop", now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Read(badge.Bytes(), sig, now.Add(Lasts+time.Hour)); err == nil {
		t.Fatal("an expired badge was believed")
	}
	if _, err := Read(badge.Bytes(), sig, now.Add(Lasts-time.Hour)); err != nil {
		t.Fatalf("a badge that had not run out was refused: %v", err)
	}
}

// Changing what a badge says has to break it, or it says nothing.
func TestAnAlteredBadgeIsRefused(t *testing.T) {
	by := aKey(t)
	now := time.Now()

	badge, sig, err := Sign(by, someDevice, "laptop", now)
	if err != nil {
		t.Fatal(err)
	}

	altered := badge
	altered.Device = "0000000000000000000000000000000000000000000000000000000000000000"

	if _, err := Read(altered.Bytes(), sig, now); err == nil {
		t.Fatal("a badge pointed at another machine was believed")
	}
}

// What was signed and what is read back must be the same bytes, whatever order anything was built.
func TestABadgeIsWrittenTheSameWayTwice(t *testing.T) {
	by := aKey(t)
	at := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	one := Badge{User: by.PublicKey(), Device: someDevice, Name: "laptop", Until: at}
	two := Badge{User: by.PublicKey(), Device: someDevice, Name: "laptop", Until: at.Local()}

	if string(one.Bytes()) != string(two.Bytes()) {
		t.Errorf("the same badge wrote itself two ways:\n%s\n%s", one.Bytes(), two.Bytes())
	}
}

var _ ssh.PublicKey = (ssh.PublicKey)(nil)
