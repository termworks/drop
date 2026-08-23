// Package rendezvous lets two paired devices find each other after one of them has moved.
//
// A device publishes where it currently is to a pkarr relay. The problem with publishing under
// your own identity is that anyone who learns it can then locate you, and every record you write
// is visibly the same device. So a record is instead published under a throwaway identity derived
// from the secret the two devices established when they paired:
//
//	identity = ed25519(HKDF(pair secret, publisher, epoch))
//
// Both sides can compute it and nobody else can, because it takes the pair secret. One device
// paired with three others publishes three unlinkable records, and the relay holding them cannot
// tell they describe one machine.
package rendezvous

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/tmc/go-iroh/key"
	"golang.org/x/crypto/hkdf"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

// Epoch is how long one derived identity lasts. Rotating means a relay cannot watch a single
// record to learn when a device comes and goes over weeks.
const Epoch = time.Hour

// Info separates this use of the pairing secret from every other. The secret also protects the
// session, and a derivation shared between the two would let one leak the other.
const Info = "drop rendezvous v1"

// EpochAt is the bucket a moment falls in.
func EpochAt(t time.Time) int64 { return t.Unix() / int64(Epoch/time.Second) }

// PublishEpochs is what a publisher writes under: this bucket and the next.
//
// Writing only the current one loses every peer that looks a moment after the hour turns. The
// buckets have to overlap, or the two sides pass each other at the boundary and neither is wrong.
func PublishEpochs(t time.Time) []int64 {
	now := EpochAt(t)
	return []int64{now, now + 1}
}

// ResolveEpochs is what a resolver reads: this bucket and the one before.
func ResolveEpochs(t time.Time) []int64 {
	now := EpochAt(t)
	return []int64{now, now - 1}
}

// Derive returns the identity a record about publisher is published under during epoch.
//
// publisher is part of the derivation, so the two ends of a pair do not derive the same identity
// and overwrite each other's record with their own address.
func Derive(secret []byte, publisher node.ID, epoch int64) (key.SecretKey, error) {
	if len(secret) != book.SecretBytes {
		return key.SecretKey{}, fmt.Errorf("a rendezvous needs a %d byte pairing secret, got %d", book.SecretBytes, len(secret))
	}

	salt := publisher.Bytes()

	var context [8]byte
	binary.BigEndian.PutUint64(context[:], uint64(epoch))

	var seed [32]byte
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, salt[:], append([]byte(Info), context[:]...)), seed[:]); err != nil {
		return key.SecretKey{}, fmt.Errorf("deriving the rendezvous identity: %w", err)
	}
	return key.NewSecretKey(seed), nil
}
