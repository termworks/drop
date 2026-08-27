// Package history is the record of what happened to one thing.
//
// A change is one thing somebody did. It names the thing it was done to and the changes its author
// had seen when they made it, so the record is a graph rather than a line, and its id is the hash
// of the bytes they signed, so a change cannot be altered without becoming a different change and
// two machines that made the same change made one change. What the changes mean is the archetype's
// business; this orders them and nothing more.
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
	// About is the thing it was done to, by the id that thing is known by everywhere. It is signed
	// with the rest, so a change made about one thing is not a change about another: replaying it
	// into some other history is refused there rather than passing as the author's own doing.
	About string
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
	// Fold is the changes this one stands in place of, smallest first and each named once. Empty
	// for all but a snapshot: a snapshot carries what those changes came to, so a machine holding
	// it may forget them and one that never held them may take it on its own.
	Fold []ID
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
const maxSigned = MaxBody + (MaxHeads+MaxHeld)*(len(ID{})+1) + 2*wire.MaxString + 64

// Sign makes a change to one thing, signed as the person sitting at this machine.
//
// The thing's id is signed with the rest, so what is signed says which history it belongs in. The
// heads given are put in order and each named once, so that one set of changes seen is one change
// rather than several spellings of it.
func Sign(about string, body []byte, heads []ID) (Change, error) {
	return sign(about, body, heads, nil)
}

// sign is that, and a snapshot besides: the same bytes, with the changes it stands for named.
func sign(about string, body []byte, heads, fold []ID) (Change, error) {
	if err := nameable(about); err != nil {
		return Change{}, fmt.Errorf("signing a change: %w", err)
	}
	if len(body) > MaxBody {
		return Change{}, fmt.Errorf("signing a change: %d bytes, over the %d limit", len(body), MaxBody)
	}
	if len(heads) > MaxHeads {
		return Change{}, fmt.Errorf("signing a change: it names %d changes, over the %d limit", len(heads), MaxHeads)
	}
	if len(fold) > MaxHeld {
		return Change{}, fmt.Errorf("signing a change: it stands for %d changes, over the %d limit", len(fold), MaxHeld)
	}

	by, err := signer()
	if err != nil {
		return Change{}, err
	}

	c := Change{
		About:  about,
		Heads:  tidy(heads),
		Author: user.Text(by.PublicKey()),
		At:     time.Now().UnixMilli(),
		Body:   append([]byte(nil), body...),
		Fold:   tidy(fold),
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

// Whole reports whether this change carries what the changes it names came to, rather than one
// more thing done on top of them. An archetype reads such a change as the state itself.
func (c Change) Whole() bool { return len(c.Fold) > 0 }

// bytes is what the author signs and what the id is taken over: everything the change says, minus
// the signature over it.
func (c Change) bytes() []byte {
	w := wire.NewWriter()
	w.String(c.About)
	w.Uint(uint64(len(c.Heads)))
	for _, head := range c.Heads {
		w.Bytes(head[:])
	}
	w.String(c.Author)
	w.Int(c.At)
	w.Bytes(c.Body)
	w.Uint(uint64(len(c.Fold)))
	for _, id := range c.Fold {
		w.Bytes(id[:])
	}
	return w.Body()
}

// unpack reads back what was signed.
func unpack(signed []byte) (Change, error) {
	var c Change

	r := wire.NewReader(signed)
	about, err := r.String(wire.MaxString)
	if err != nil {
		return c, err
	}

	heads, err := run(r, MaxHeads)
	if err != nil {
		return Change{}, err
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

	fold, err := run(r, MaxHeld)
	if err != nil {
		return Change{}, err
	}
	if !r.Done() {
		return Change{}, fmt.Errorf("that change has %d bytes nobody claims", len(signed))
	}

	c.About, c.Heads, c.Author, c.At = about, heads, author, at
	c.Body, c.Fold = append([]byte(nil), body...), fold
	return c, nil
}

// run reads a run of change ids, refusing a count nobody could mean before any of it is kept.
func run(r *wire.Reader, most int) ([]ID, error) {
	count, err := r.Uint()
	if err != nil {
		return nil, err
	}
	if count > uint64(most) {
		return nil, fmt.Errorf("that change names %d changes, over the %d limit", count, most)
	}
	if count == 0 {
		return nil, nil
	}

	out := make([]ID, 0, min(int(count), 1<<10))
	for range count {
		id, err := r.Bytes(len(ID{}))
		if err != nil {
			return nil, err
		}
		if len(id) != len(ID{}) {
			return nil, fmt.Errorf("that change names an id of %d bytes", len(id))
		}
		out = append(out, ID(id))
	}
	return out, nil
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

// Encode is one change as it travels: its id, the bytes its author signed, and the signature.
// Nothing is added for the wire and nothing is left off, so what arrives is what was signed.
func (c Change) Encode() []byte { return record(c) }

// Decode reads one change off the wire, refusing one that is not written the way a change is
// written. Whether the person it names really signed it is asked when it is taken, and whether
// they were allowed to is asked by whoever holds the access rule.
func Decode(raw []byte) (Change, error) { return unrecord(raw) }

// record is one change as it is written down.
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

// unrecord reads one, refusing one that is not written the way it was written: an id that does not
// match the bytes beside it, or bytes that would not be written back as they arrived.
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

// tidy is a set of ids in the one order they are written: smallest first, each named once.
func tidy(ids []ID) []ID {
	if len(ids) == 0 {
		return nil
	}

	out := append([]ID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })

	kept := out[:1]
	for _, id := range out[1:] {
		if id != kept[len(kept)-1] {
			kept = append(kept, id)
		}
	}
	return kept
}

// tidied reports whether ids are already written that way.
func tidied(ids []ID) bool {
	for i := 1; i < len(ids); i++ {
		if bytes.Compare(ids[i-1][:], ids[i][:]) >= 0 {
			return false
		}
	}
	return true
}

// names reports whether a tidied run of ids holds one.
func names(ids []ID, id ID) bool {
	at := sort.Search(len(ids), func(i int) bool { return bytes.Compare(ids[i][:], id[:]) >= 0 })
	return at < len(ids) && ids[at] == id
}
