package proto

import (
	"errors"
	"fmt"

	"github.com/bresilla/drop/src/pkg/wire"
)

// MaxSecret bounds what may be offered as a password.
//
// A password is a thing somebody types, and checking one costs 64 MiB and three passes of argon2 —
// so the length is worth refusing before any of that work is done. This field also carries what an
// ask says about itself, which MaxWhy cuts shorter still.
const MaxSecret = 1024

// Opening is the first frame of a session, and the only one this package writes on the way in.
//
// It names a path and, when the caller knows what it expects to find there, an archetype. What is
// said after the far end accepts belongs to that archetype, and nothing here reads a byte of it.
type Opening struct {
	// Archetype is what the caller expects at the path, and Version which revision of it. An empty
	// name asks for whatever is mounted, which is what somebody typing a path rather than reading
	// a listing is doing.
	Archetype string
	Version   int
	// Path is the namespace being opened.
	Path string
	From string
	// Secret is a password offered for a path that asks for one. Empty when none was given.
	Secret string
	// Ask says this is not an open at all: it rings the bell on a path the caller can see and
	// cannot open, and Secret carries whatever they said about why.
	Ask bool
	// Meet says this is not an open either: the caller holds the same namespace and wants to catch
	// up with it. What is said afterwards is heads and changes rather than anything the archetype
	// would recognise.
	//
	// Held is which namespace, by the name every machine holding it works out for itself. A meeting
	// is about a thing rather than about a path: a path is one machine's own word for it, and two
	// machines that spell it differently would otherwise catch up about two different things.
	Meet bool
	Held string
	// Badge says whose machine this is, and Signed is the proof of it.
	//
	// The transport already proves which machine is calling. This is what turns that into a
	// person, so a rule can name one.
	Badge  []byte
	Signed []byte
	// Plate says which machine this drop is running on, and Stamped is the proof of it.
	//
	// Separate from the badge because it answers a different question. The badge says whose drop
	// this is; the plate says what it is sitting on, so several people with accounts on one
	// machine are seen as one machine with several people rather than as unrelated machines.
	Plate   []byte
	Stamped []byte
	// Moved says this machine is the one another machine became, and Handed is the proof of it.
	//
	// Carried on every opening because there is no telling which peer has not heard yet, and a
	// peer that has heard does nothing with it. It is signed by the old machine, so what it costs
	// somebody who was never told is one signature check they refuse.
	Moved  []byte
	Handed []byte
}

func (o Opening) encode() []byte {
	w := wire.NewWriter()
	w.Bool(o.Ask)
	w.Bool(o.Meet)
	w.String(o.Archetype)
	w.Uint(uint64(o.Version))
	w.String(o.From)
	w.String(o.Path)
	w.String(o.Held)
	w.String(o.Secret)
	w.String(string(o.Badge))
	w.String(string(o.Signed))
	w.String(string(o.Plate))
	w.String(string(o.Stamped))
	w.String(string(o.Moved))
	w.String(string(o.Handed))
	return w.Body()
}

// MaxHeld bounds the name of a namespace several machines hold. It is a hash written in hex, and a
// longer one names nothing this node could be holding.
const MaxHeld = 128

// what is the namespace an opening is about, in the words its failures use.
func (o Opening) what() string {
	if o.Meet {
		return "the namespace named " + o.Held
	}
	return o.Path
}

// MaxVersion bounds the revision a caller may name, so a number off the wire stays a number.
const MaxVersion = 1 << 16

func decodeOpen(body []byte) (Opening, error) {
	var out Opening

	r := wire.NewReader(body)
	ask, err := r.Bool()
	if err != nil {
		return out, err
	}
	meet, err := r.Bool()
	if err != nil {
		return out, err
	}
	archetype, err := r.String(256)
	if err != nil {
		return out, err
	}
	version, err := r.Uint()
	if err != nil {
		return out, err
	}
	if version > MaxVersion {
		return out, fmt.Errorf("an open asks for version %d, over the %d limit", version, MaxVersion)
	}
	from, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	path, err := r.String(1024)
	if err != nil {
		return out, err
	}
	held, err := r.String(MaxHeld)
	if err != nil {
		return out, err
	}
	secret, err := r.String(MaxSecret)
	if err != nil {
		return out, err
	}
	badge, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	signed, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	plate, err := r.String(MaxSigned)
	if err != nil {
		return out, err
	}
	stamped, err := r.String(MaxSignature)
	if err != nil {
		return out, err
	}
	moved, err := r.String(MaxSigned)
	if err != nil {
		return out, err
	}
	handed, err := r.String(MaxSignature)
	if err != nil {
		return out, err
	}

	out.Ask, out.Meet, out.Archetype, out.Version = ask, meet, archetype, int(version)
	out.From, out.Path, out.Held, out.Secret = from, path, held, secret
	out.Badge, out.Signed = []byte(badge), []byte(signed)
	out.Plate, out.Stamped = []byte(plate), []byte(stamped)
	out.Moved, out.Handed = []byte(moved), []byte(handed)
	return out, nil
}

// Declined is a far end that answered and said no.
//
// Worth telling apart from a far end that could not be reached: one is a device that is off, which
// is what a queue is for, and the other is an answer. Queueing an answer means retrying forever
// against a decision somebody made.
type Declined struct {
	Reason string
	// Settled says the far end decided about this caller rather than being unable to answer.
	Settled bool
}

func (d Declined) Error() string { return "declined: " + d.Reason }

// Settled reports whether a refusal was a decision that asking again will not change.
func Settled(err error) bool {
	var declined Declined
	return errors.As(err, &declined) && declined.Settled
}

// WasDeclined reports whether an error is a refusal rather than a failure to reach anybody.
func WasDeclined(err error) bool {
	var declined Declined
	return errors.As(err, &declined)
}

// What a plate and a handover may weigh on the wire.
//
// Both are a handful of fixed lines and an id or two — a few hundred bytes at the outside. The
// general string limit is sixty-four kilobytes, which for these is sixty-four kilobytes of somebody
// else's choosing that has to be parsed before anybody has been authenticated. Bounding them to
// what they can actually be turns that into a length check.
const (
	MaxSigned    = 1024
	MaxSignature = 64
)
