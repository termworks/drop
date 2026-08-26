// Package history is the record of what happened to one thing.
//
// A change is one thing somebody did. It names the changes its author had seen when they made it,
// so the record is a graph rather than a line, and its id is the hash of the bytes they signed, so
// a change cannot be altered without becoming a different change and two machines that made the
// same change made one change. What the changes mean is the archetype's business; this orders them
// and nothing more.
package history

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/user"
	"github.com/bresilla/drop/src/pkg/wire"
)

// ID is a change's name: blake3 over the bytes its author signed.
type ID [32]byte

// String is an id as it is written down and read back.
func (id ID) String() string { return hex.EncodeToString(id[:]) }

// Change is one thing somebody did to one thing.
type Change struct {
	// Heads is what its author had already seen, written smallest first and each named once.
	// Empty for the first change there was.
	Heads []ID
	// Author is the person who made it: their user key, written the way authorized_keys writes one.
	Author string
	// At is when its author says it was made, in milliseconds. Nothing is ordered by it — there is
	// no clock between two machines to appeal to, and a wrong one must not reorder anybody's
	// history. It is carried for people to read.
	At int64
	// Body is the archetype's own business, opaque here.
	Body []byte
	// Signed is the author's signature over everything above.
	Signed []byte
}

// What one change may weigh.
const (
	// MaxBody caps a change's payload. A change is an edit, not a file: bytes of their own travel
	// on the path files already take.
	MaxBody = 1 << 20
	// MaxHeads caps how many changes one may name. That is how many ways a thing was being edited
	// at once, and past a few thousand it is not a merge.
	MaxHeads = 1 << 12
	// maxSignature caps a signature blob, which is a key, a namespace and a few hundred bytes.
	maxSignature = 1 << 14
)

// maxSigned caps the half of a record the author signed.
const maxSigned = MaxBody + MaxHeads*(len(ID{})+1) + wire.MaxString + 64

// Sign makes a change, signed as the person sitting at this machine.
//
// The heads given are put in order and each named once, so that one set of changes seen is one
// change rather than several spellings of it.
func Sign(body []byte, heads []ID) (Change, error) {
	if len(body) > MaxBody {
		return Change{}, fmt.Errorf("signing a change: %d bytes, over the %d limit", len(body), MaxBody)
	}
	if len(heads) > MaxHeads {
		return Change{}, fmt.Errorf("signing a change: it names %d changes, over the %d limit", len(heads), MaxHeads)
	}

	by, err := signer()
	if err != nil {
		return Change{}, err
	}

	c := Change{
		Heads:  tidy(heads),
		Author: user.Text(by.PublicKey()),
		At:     time.Now().UnixMilli(),
		Body:   append([]byte(nil), body...),
	}
	sig, err := user.Signature(by, c.bytes())
	if err != nil {
		return Change{}, fmt.Errorf("signing a change: %w", err)
	}
	c.Signed = sig
	return c, nil
}

// signer is the person this machine signs as.
//
// A change is authored by a person rather than by a machine, because the same person edits from
// several of them and a third has to be able to check what they wrote. A machine with no user key
// at all says so: making one up here would author the change as nobody.
func signer() (ssh.Signer, error) {
	where, err := user.Where()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(where); err != nil {
		return nil, fmt.Errorf("signing a change: there is no user key at %s", where)
	}
	return user.Signer()
}

// ID is what this change is called.
func (c Change) ID() ID { return blake3.Sum256(c.bytes()) }

// bytes is what the author signs and what the id is taken over: everything the change says, minus
// the signature over it.
func (c Change) bytes() []byte {
	w := wire.NewWriter()
	w.Uint(uint64(len(c.Heads)))
	for _, head := range c.Heads {
		w.Bytes(head[:])
	}
	w.String(c.Author)
	w.Int(c.At)
	w.Bytes(c.Body)
	return w.Body()
}

// unpack reads back what was signed.
func unpack(signed []byte) (Change, error) {
	var c Change

	r := wire.NewReader(signed)
	count, err := r.Uint()
	if err != nil {
		return c, err
	}
	if count > MaxHeads {
		return c, fmt.Errorf("that change names %d changes, over the %d limit", count, MaxHeads)
	}

	c.Heads = make([]ID, 0, wire.Hint(count, signed, len(ID{})+1))
	for range count {
		head, err := r.Bytes(len(ID{}))
		if err != nil {
			return Change{}, err
		}
		if len(head) != len(ID{}) {
			return Change{}, fmt.Errorf("that change names an id of %d bytes", len(head))
		}
		c.Heads = append(c.Heads, ID(head))
	}

	author, err := r.String(wire.MaxString)
	if err != nil {
		return Change{}, err
	}
	at, err := r.Int()
	if err != nil {
		return Change{}, err
	}
	body, err := r.Bytes(MaxBody)
	if err != nil {
		return Change{}, err
	}
	if !r.Done() {
		return Change{}, fmt.Errorf("that change has %d bytes nobody claims", len(signed))
	}

	c.Author, c.At, c.Body = author, at, append([]byte(nil), body...)
	return c, nil
}

// verify reports whether a change was really signed by the person it names.
//
// Whether that person was allowed to change this thing is somebody else's question: permission is
// the access rule's business and it lives elsewhere.
func verify(c Change) error {
	if len(c.Signed) == 0 {
		return fmt.Errorf("that change is not signed")
	}
	who, err := user.Verify(c.Signed, c.bytes(), user.Namespace)
	if err != nil {
		return fmt.Errorf("checking who made it: %w", err)
	}
	if user.Text(who) != c.Author {
		return fmt.Errorf("it names %q but was signed by %s", strings.TrimSpace(c.Author), user.Fingerprint(who))
	}
	return nil
}

// Encode is one change as it travels, which is exactly how it is stored: nothing is added for the
// wire and nothing is left off, so a change that arrives is written down as it came.
func (c Change) Encode() []byte { return record(c) }

// Decode reads one change off the wire, refusing one that is not written the way a change is
// written. Whether the person it names really signed it is asked when it is taken, and whether
// they were allowed to is asked by whoever holds the access rule.
func Decode(raw []byte) (Change, error) { return unrecord(raw) }

// record is one change as it is stored: its id, the bytes its author signed, and the signature.
//
// The id is written down so that reading the log back costs a hash rather than a signature check
// per change. A record whose bytes were altered no longer hashes to the id beside it, and it is
// refused there.
func record(c Change) []byte {
	id := c.ID()
	w := wire.NewWriter()
	w.Bytes(id[:])
	w.Bytes(c.bytes())
	w.Bytes(c.Signed)
	return w.Body()
}

// unrecord reads one stored change, refusing one that is not written the way it was written: an id
// that does not match the bytes beside it, or bytes that would not be written back as they arrived.
func unrecord(raw []byte) (Change, error) {
	r := wire.NewReader(raw)
	named, err := r.Bytes(len(ID{}))
	if err != nil {
		return Change{}, err
	}
	if len(named) != len(ID{}) {
		return Change{}, fmt.Errorf("that record is named by %d bytes", len(named))
	}
	signed, err := r.Bytes(maxSigned)
	if err != nil {
		return Change{}, err
	}
	sig, err := r.Bytes(maxSignature)
	if err != nil {
		return Change{}, err
	}
	if !r.Done() {
		return Change{}, fmt.Errorf("that record has %d bytes nobody claims", len(raw))
	}

	c, err := unpack(signed)
	if err != nil {
		return Change{}, err
	}
	if !bytes.Equal(c.bytes(), signed) {
		return Change{}, fmt.Errorf("that change is not written the way a change is written")
	}

	id := c.ID()
	if id != ID(named) {
		return Change{}, fmt.Errorf("that record is filed as %s but its bytes are %s", ID(named), id)
	}

	c.Signed = append([]byte(nil), sig...)
	return c, nil
}

// tidy is a set of heads in the one order they are written: smallest first, each named once.
func tidy(heads []ID) []ID {
	if len(heads) == 0 {
		return nil
	}

	out := append([]ID(nil), heads...)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })

	kept := out[:1]
	for _, head := range out[1:] {
		if head != kept[len(kept)-1] {
			kept = append(kept, head)
		}
	}
	return kept
}

// tidied reports whether heads are already written that way.
func tidied(heads []ID) bool {
	for i := 1; i < len(heads); i++ {
		if bytes.Compare(heads[i-1][:], heads[i][:]) >= 0 {
			return false
		}
	}
	return true
}
