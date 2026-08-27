package history

import (
	"crypto/rand"
	"fmt"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/bresilla/drop/src/pkg/wire"
)

// What is kept on disk, and whether anybody can read it.
//
// A history holds somebody's notes and the contents of the folders they share, so it is sealed the
// way a conversation is: the log stays a file of length-prefixed records, each record's payload
// sealed on its own, and reading is still a walk. The key is the same data key the vault unwraps.
//
// Each record is bound to the thing the change is about and to the change's own id, which is the
// same pair the signature covers. A record cannot be lifted into another thing's log or put back
// somewhere else in this one: either move makes the tag wrong.

// sealedMark is the first byte of a sealed record. A record in the clear starts with the length of
// the id that names it, which is 32, so a byte that is neither says this one is sealed.
const sealedMark byte = 0xE1

// maxCipher caps a sealed payload: the largest record there can be, and the tag the seal adds.
const maxCipher = len(ID{}) + maxSigned + maxSignature + 3*binaryHead + 32

// binaryHead is what a length prefix costs inside a record.
const binaryHead = 10

var held struct {
	sync.Mutex
	key   []byte
	get   func() ([]byte, error)
	asked bool
	err   error
}

// Unlock holds the data key for as long as this process runs. Called with what the vault unwrapped,
// which is the same key the conversations are kept under.
func Unlock(key []byte) {
	held.Lock()
	defer held.Unlock()

	held.key, held.get, held.asked, held.err = key, nil, true, nil
}

// Unlocking says how to get the data key, without getting it.
//
// Most commands never touch a history, and opening a vault means parsing a config and unwrapping a
// key. So the way to the key is handed over at startup and followed the first time something
// actually reads or writes a change.
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

// sealCost is what sealing adds to a record: the mark, the id, the nonce and the tag.
const sealCost = 128

// stored is one record as it is kept: as it was written, sealed if there is a key.
func stored(body []byte, about string, id ID) ([]byte, error) {
	key, err := keyed()
	if err != nil {
		// A key that exists and cannot be reached must not turn into plaintext appended to a sealed
		// log. Refusing to write is the only safe answer.
		return nil, err
	}
	if len(key) == 0 {
		return body, nil
	}
	return seal(key, body, about, id)
}

// unstored reads one back, opening it first when it was sealed.
func unstored(raw []byte, about string) (Change, error) {
	if !isSealed(raw) {
		return unrecord(raw)
	}

	key, err := keyed()
	if err != nil {
		return Change{}, err
	}
	if len(key) == 0 {
		return Change{}, ErrLocked
	}
	opened, err := unseal(key, raw, about)
	if err != nil {
		return Change{}, err
	}
	return unrecord(opened)
}

// seal encrypts one record.
func seal(key, body []byte, about string, id ID) ([]byte, error) {
	box, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("sealing a change: %w", err)
	}

	nonce := make([]byte, box.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("sealing a change: %w", err)
	}

	w := wire.NewWriter()
	w.Byte(sealedMark)
	w.Bytes(id[:])
	w.Bytes(nonce)
	w.Bytes(box.Seal(nil, nonce, body, bound(about, id)))
	return w.Body(), nil
}

// unseal opens one, given the key that sealed it.
func unseal(key, kept []byte, about string) ([]byte, error) {
	r := wire.NewReader(kept)
	if _, err := r.Byte(); err != nil {
		return nil, err
	}
	named, err := r.Bytes(len(ID{}))
	if err != nil {
		return nil, err
	}
	if len(named) != len(ID{}) {
		return nil, fmt.Errorf("that record is named by %d bytes", len(named))
	}
	nonce, err := r.Bytes(chacha20poly1305.NonceSizeX)
	if err != nil {
		return nil, err
	}
	body, err := r.Bytes(maxCipher)
	if err != nil {
		return nil, err
	}

	box, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("opening a change: %w", err)
	}
	out, err := box.Open(nil, nonce, body, bound(about, ID(named)))
	if err != nil {
		return nil, fmt.Errorf("opening a change: %w", err)
	}
	return out, nil
}

// isSealed reports whether a stored record was encrypted.
func isSealed(kept []byte) bool { return len(kept) > 0 && kept[0] == sealedMark }

// bound is what a record is tied to: this thing, and this change.
func bound(about string, id ID) []byte { return append([]byte(about+"\x00"), id[:]...) }

// ErrLocked is a sealed record on a device whose data key is not held.
//
// It matters that this is not "no changes": the history is there, and saying it is empty would be
// a lie about the disk rather than a report about the key.
var ErrLocked = fmt.Errorf("this device is locked")
