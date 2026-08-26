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
	// Badge says whose machine this is, and Signed is the proof of it.
	//
	// The transport already proves which machine is calling. This is what turns that into a
	// person, so a rule can name one.
	Badge  []byte
	Signed []byte
}

func (o Opening) encode() []byte {
	w := wire.NewWriter()
	w.Bool(o.Ask)
	w.String(o.Archetype)
	w.Uint(uint64(o.Version))
	w.String(o.From)
	w.String(o.Path)
	w.String(o.Secret)
	w.String(string(o.Badge))
	w.String(string(o.Signed))
	return w.Body()
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

	out.Ask, out.Archetype, out.Version = ask, archetype, int(version)
	out.From, out.Path, out.Secret = from, path, secret
	out.Badge, out.Signed = []byte(badge), []byte(signed)
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
