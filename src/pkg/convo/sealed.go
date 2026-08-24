package convo

import (
	"crypto/rand"
	"fmt"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/bresilla/drop/src/pkg/wire"
)

// What is kept on disk, and whether anybody can read it.
//
// A conversation is a file of length-prefixed records, and without a key `strings` reads it. With
// one, each record's payload is sealed on its own -- the log stays append-only, a crash still
// truncates the tail rather than corrupting what came before, and reading is still a walk.
//
// The key is held for the process rather than passed through every call, because there is exactly
// one of it: the daemon unwraps it at startup and holds it until it stops. Threading it through
// every Open would mean every caller that forgot became a caller that quietly wrote plaintext.

// sealedMark is the first byte of a sealed record.
//
// A record has always started with its direction, which is 1 or 2, so a byte that is neither says
// this record is sealed without any format version having to be invented. A log written before
// vaults existed reads exactly as it did.
const sealedMark byte = 0xE1

var held struct {
	sync.Mutex
	key   []byte
	get   func() ([]byte, error)
	asked bool
	err   error
}

// Unlock holds the data key for as long as this process runs. Called with what the vault
// unwrapped. Nothing at all leaves the history in the clear, which is the default.
func Unlock(key []byte) {
	held.Lock()
	defer held.Unlock()

	held.key, held.get, held.asked, held.err = key, nil, true, nil
}

// Unlocking says how to get the data key, without getting it.
//
// Most commands never touch a conversation, and opening a vault means parsing a config and
// unwrapping a key -- work that would be done by `drop id` for no reason. So the way to the key is
// handed over at startup and followed the first time something actually reads or writes history.
func Unlocking(get func() ([]byte, error)) {
	held.Lock()
	defer held.Unlock()

	held.key, held.get, held.asked, held.err = nil, get, false, nil
}

// keyed is the data key, and nil when there is none. An error is a device whose key exists and
// could not be reached, which is a different thing from a device that has none.
func keyed() ([]byte, error) {
	held.Lock()
	defer held.Unlock()

	if !held.asked && held.get != nil {
		held.key, held.err = held.get()
		held.asked = true
	}
	return held.key, held.err
}

// seal encrypts one record's payload.
//
// The peer and the message id go in as associated data, so a record cannot be lifted into another
// conversation or put back after being taken out: either move makes the tag wrong.
func seal(key, body []byte, peer, id string) ([]byte, error) {
	box, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("sealing a record: %w", err)
	}

	nonce := make([]byte, box.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("sealing a record: %w", err)
	}

	w := wire.NewWriter()
	w.Byte(sealedMark)
	w.String(id)
	w.Bytes(nonce)
	w.Bytes(box.Seal(nil, nonce, body, bound(peer, id)))
	return w.Body(), nil
}

// unseal opens one, given the key that sealed it.
func unseal(key, stored []byte, peer string) ([]byte, error) {
	r := wire.NewReader(stored)
	if _, err := r.Byte(); err != nil {
		return nil, err
	}
	id, err := r.String(wire.MaxString)
	if err != nil {
		return nil, err
	}
	nonce, err := r.Bytes(chacha20poly1305.NonceSizeX)
	if err != nil {
		return nil, err
	}
	body, err := r.Bytes(MaxBody + 4096)
	if err != nil {
		return nil, err
	}

	box, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("opening a record: %w", err)
	}
	out, err := box.Open(nil, nonce, body, bound(peer, id))
	if err != nil {
		return nil, fmt.Errorf("opening a record: %w", err)
	}
	return out, nil
}

// isSealed reports whether a stored record was encrypted.
func isSealed(stored []byte) bool { return len(stored) > 0 && stored[0] == sealedMark }

// bound is what a record is tied to: this conversation, and this message.
func bound(peer, id string) []byte { return []byte(peer + "\x00" + id) }

// ErrLocked is a sealed record on a device whose data key is not held.
//
// It matters that this is not "no messages": the history is there, and saying it is empty would be
// a lie about the disk rather than a report about the key.
var ErrLocked = fmt.Errorf("this device is locked")
