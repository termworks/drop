package rendezvous

import (
	"bytes"
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

func id(seed byte) node.ID {
	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

func secret(seed byte) []byte {
	s := make([]byte, book.SecretBytes)
	for i := range s {
		s[i] = seed
	}
	return s
}

// Both devices have to arrive at the same identity from their own copy of the secret, or one
// publishes where the other never looks.
func TestBothSidesDeriveTheSameIdentity(t *testing.T) {
	alice, bob := secret(1), secret(1)

	a, err := Derive(alice, id(7), 100)
	if err != nil {
		t.Fatalf("deriving: %v", err)
	}
	b, err := Derive(bob, id(7), 100)
	if err != nil {
		t.Fatalf("deriving: %v", err)
	}

	if a.Public().EndpointID() != b.Public().EndpointID() {
		t.Fatal("the two sides derived different identities")
	}
}

// The two ends of one pair must not derive the same identity, or whichever publishes second
// overwrites the other's address with its own and both become unreachable.
func TestTheTwoEndsOfAPairDiffer(t *testing.T) {
	s := secret(1)

	a, _ := Derive(s, id(7), 100)
	b, _ := Derive(s, id(9), 100)

	if a.Public().EndpointID() == b.Public().EndpointID() {
		t.Fatal("both ends of the pair publish to the same record")
	}
}

// This is the property the whole design exists for. One device paired with two others must look
// like two unrelated publishers, or the relay can tell they are the same machine.
func TestRecordsAreUnlinkableAcrossPairs(t *testing.T) {
	me := id(7)

	withOne, _ := Derive(secret(1), me, 100)
	withTwo, _ := Derive(secret(2), me, 100)

	if withOne.Public().EndpointID() == withTwo.Public().EndpointID() {
		t.Fatal("the same identity is published to two different peers")
	}
}

func TestTheIdentityRotates(t *testing.T) {
	s := secret(1)

	now, _ := Derive(s, id(7), 100)
	later, _ := Derive(s, id(7), 101)

	if now.Public().EndpointID() == later.Public().EndpointID() {
		t.Fatal("the identity did not change with the epoch")
	}
}

// Without the secret the identity cannot be computed, which is what stops anyone who merely knows
// a device's real ID from locating it.
func TestADifferentSecretGivesADifferentIdentity(t *testing.T) {
	a, _ := Derive(secret(1), id(7), 100)
	b, _ := Derive(secret(2), id(7), 100)

	if a.Public().EndpointID() == b.Public().EndpointID() {
		t.Fatal("the secret did not affect the identity")
	}
}

func TestDeriveIsDeterministic(t *testing.T) {
	a, _ := Derive(secret(3), id(4), 55)
	b, _ := Derive(secret(3), id(4), 55)

	ab, bb := a.Public().EndpointID().Bytes(), b.Public().EndpointID().Bytes()
	if !bytes.Equal(ab[:], bb[:]) {
		t.Fatal("two derivations of the same inputs disagreed")
	}
}

func TestDeriveRefusesAWrongSizedSecret(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		if _, err := Derive(make([]byte, n), id(1), 1); err == nil {
			t.Fatalf("a %d byte secret was accepted", n)
		}
	}
}

// An hour boundary is where a publisher and a resolver most easily miss each other: one writes
// under the hour it is leaving while the other reads the hour it has entered.
func TestThePublishAndResolveWindowsOverlap(t *testing.T) {
	// A second before the hour turns, and a second after.
	before := time.Unix(99*3600+3599, 0)
	after := time.Unix(100*3600+1, 0)

	published := PublishEpochs(before)
	resolved := ResolveEpochs(after)

	if !shares(published, resolved) {
		t.Fatalf("no overlap across the boundary: published %v, resolved %v", published, resolved)
	}
}

func TestTheWindowsOverlapWithinAnHour(t *testing.T) {
	at := time.Unix(100*3600+120, 0)

	if !shares(PublishEpochs(at), ResolveEpochs(at)) {
		t.Fatal("no overlap inside one epoch")
	}
}

func shares(a, b []int64) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func TestEpochAdvancesOncePerHour(t *testing.T) {
	base := time.Unix(100*3600, 0)

	if EpochAt(base) != EpochAt(base.Add(59*time.Minute)) {
		t.Fatal("the epoch changed inside one hour")
	}
	if EpochAt(base) == EpochAt(base.Add(time.Hour)) {
		t.Fatal("the epoch did not change after an hour")
	}
}
