// Package convo is the conversation between this node and one peer: an ordered, durable log of
// everything that passed between them, whatever modality it arrived in.
package convo

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/wire"
)

// What a message is.
const (
	// KindText is something a person typed.
	KindText byte = 1
	// KindLink is a URL, which the receiving side may be configured to open.
	KindLink byte = 2
	// KindEvent is the conversation narrating itself: a file arrived, a cast started. Written by
	// drop rather than by a person, so a log reads as one story instead of only the chat half.
	KindEvent byte = 3
	// KindFile records that a file changed hands. The body is its name; Extra carries the size.
	KindFile byte = 4
)

// Direction of a message relative to this node.
const (
	Out byte = 1
	In  byte = 2
)

// Message is one entry in a conversation.
type Message struct {
	ID   string
	Kind byte
	Dir  byte
	Body string
	// Extra carries whatever the kind needs and text does not: a file size, a stream name.
	Extra string
	// At is the sending node's clock, in milliseconds. Two nodes never agree exactly, so it orders
	// a conversation but never decides identity — the ID does that.
	At int64
}

// When is At as a time.
func (m Message) When() time.Time {
	return time.UnixMilli(m.At)
}

// idBytes is 6 bytes of milliseconds and 10 of tail, which is the ULID layout: time first so
// lexical order is time order, and enough tail that two nodes do not collide.
const idBytes = 16

var (
	idMu       sync.Mutex
	idLastMs   int64
	idLastTail [10]byte
)

// NewID mints a sortable, unique message id.
//
// Strictly increasing, not merely time-ordered. Two messages composed in the same
// millisecond would otherwise sort by a random tail, and a log that sorts by id would show
// two quickly typed lines in the wrong order. A clock that steps backwards is handled the
// same way, because an id smaller than the last one has the same effect.
func NewID() (string, error) {
	idMu.Lock()
	defer idMu.Unlock()

	now := time.Now().UnixMilli()
	if now > idLastMs {
		idLastMs = now
		if _, err := rand.Read(idLastTail[:]); err != nil {
			return "", fmt.Errorf("minting a message id: %w", err)
		}
	} else if !bumpTail(&idLastTail) {
		// The tail wrapped, which takes 2^80 ids in one millisecond. Borrowing from the next
		// millisecond keeps the sequence rising.
		idLastMs++
		if _, err := rand.Read(idLastTail[:]); err != nil {
			return "", fmt.Errorf("minting a message id: %w", err)
		}
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(idLastMs))

	var raw [idBytes]byte
	copy(raw[0:6], stamp[2:8])
	copy(raw[6:], idLastTail[:])

	return base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]), nil
}

// bumpTail adds one to the tail, reporting false if it wrapped past its width.
func bumpTail(tail *[10]byte) bool {
	for i := len(tail) - 1; i >= 0; i-- {
		tail[i]++
		if tail[i] != 0 {
			return true
		}
	}
	return false
}

// New builds an outgoing message.
func New(kind byte, body, extra string) (Message, error) {
	id, err := NewID()
	if err != nil {
		return Message{}, err
	}
	return Message{
		ID:    id,
		Kind:  kind,
		Dir:   Out,
		Body:  body,
		Extra: extra,
		At:    time.Now().UnixMilli(),
	}, nil
}

// Encode packs a message for the wire or the log. Same encoding for both, so what is stored is
// what was sent.
func (m Message) Encode() []byte {
	w := wire.NewWriter()
	w.String(m.ID)
	w.Byte(m.Kind)
	w.String(m.Body)
	w.String(m.Extra)
	w.Int(m.At)
	return w.Body()
}

// MaxBody caps a single message. Anything larger is a file, and files have their own channel.
const MaxBody = 1 << 20

func Decode(body []byte) (Message, error) {
	var out Message

	r := wire.NewReader(body)
	id, err := r.String(256)
	if err != nil {
		return out, err
	}
	kind, err := r.Byte()
	if err != nil {
		return out, err
	}
	text, err := r.String(MaxBody)
	if err != nil {
		return out, err
	}
	extra, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	at, err := r.Int()
	if err != nil {
		return out, err
	}

	out = Message{ID: id, Kind: kind, Body: text, Extra: extra, At: at}
	return out, nil
}
