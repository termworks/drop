package proto

import (
	"errors"
	"fmt"

	"github.com/bresilla/drop/src/pkg/wire"
)

// What a session is for.
const (
	// ModeFiles is one side pushing items to the other, each ending with a digest.
	ModeFiles byte = 1
	// ModeDuplex is both sides writing until one of them stops. No sizes, no digests.
	ModeDuplex byte = 2
)

// SizeUnknown is the size of an item whose length is not known before it is sent: a pipe, a
// growing file, a terminal. The receiver reads until the item ends rather than counting down.
const SizeUnknown int64 = -1

// Item describes one thing being sent. Name is a base name; a sender does not choose where on the
// receiving machine its bytes land.
type Item struct {
	Name string
	Size int64
	Mode uint32
}

// Known reports whether this item's length was settled before sending.
func (i Item) Known() bool {
	return i.Size >= 0
}

// Open is the first frame of a session.
type Open struct {
	Mode byte
	From string
	// Path is the namespace being opened. What is there decides what happens, which is why
	// a sender does not have to name a mode the far end might not serve.
	Path  string
	Items []Item
	// Secret is a password offered for a path that asks for one. Empty when none was given.
	Secret string
	// Badge says whose machine this is, and Signed is the proof of it. Empty from a device that
	// has no user, which is every device paired before users existed.
	//
	// The transport already proves which machine is calling. This is what turns that into a
	// person, so a rule can name one.
	Badge  []byte
	Signed []byte
}

func (o Open) encode() []byte {
	w := wire.NewWriter()
	w.Byte(o.Mode)
	w.String(o.From)
	w.String(o.Path)
	w.String(o.Secret)
	w.Uint(uint64(len(o.Items)))
	for _, item := range o.Items {
		w.String(item.Name)
		w.Int(item.Size)
		w.Uint(uint64(item.Mode))
	}

	// After everything an older node knows how to read, so one that does not understand badges
	// stops here and is none the wiser.
	w.String(string(o.Badge))
	w.String(string(o.Signed))

	return w.Body()
}

// maxItems caps how many entries one offer may carry, so a small frame cannot ask for a huge slice.
const maxItems = 1 << 16

func decodeOpen(body []byte) (Open, error) {
	var out Open

	r := wire.NewReader(body)
	mode, err := r.Byte()
	if err != nil {
		return out, err
	}
	from, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	path, err := r.String(1024)
	if err != nil {
		return out, err
	}
	secret, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	out.Secret = secret

	count, err := r.Uint()
	if err != nil {
		return out, err
	}
	if count > maxItems {
		return out, fmt.Errorf("open claims %d items, over the %d limit", count, maxItems)
	}

	out.Mode, out.From, out.Path = mode, from, path
	out.Items = make([]Item, 0, count)
	for i := uint64(0); i < count; i++ {
		name, err := r.String(wire.MaxString)
		if err != nil {
			return out, err
		}
		size, err := r.Int()
		if err != nil {
			return out, err
		}
		perm, err := r.Uint()
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, Item{Name: name, Size: size, Mode: uint32(perm)})
	}

	// A node from before badges stops here, which is a node whose caller has no user rather than
	// a broken one.
	if r.Done() {
		return out, nil
	}

	badge, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	signed, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	out.Badge, out.Signed = []byte(badge), []byte(signed)
	return out, nil
}

// Accept answers an Open. Resume carries, per item, how many bytes the receiver already holds; it
// is zero for an item whose size is unknown, because there is nothing to resume against.
type Accept struct {
	Resume []int64
}

func (a Accept) encode() []byte {
	w := wire.NewWriter()
	w.Uint(uint64(len(a.Resume)))
	for _, at := range a.Resume {
		w.Int(at)
	}
	return w.Body()
}

func decodeAccept(body []byte) (Accept, error) {
	var out Accept

	r := wire.NewReader(body)
	count, err := r.Uint()
	if err != nil {
		return out, err
	}
	if count > maxItems {
		return out, fmt.Errorf("accept claims %d items, over the %d limit", count, maxItems)
	}

	out.Resume = make([]int64, 0, count)
	for i := uint64(0); i < count; i++ {
		at, err := r.Int()
		if err != nil {
			return out, err
		}
		out.Resume = append(out.Resume, at)
	}
	return out, nil
}

// End closes one item: how much was actually sent, and the digest of all of it. For an unknown-size
// item this is where the length is finally learned.
type End struct {
	Size   int64
	Digest []byte
}

func (e End) encode() []byte {
	w := wire.NewWriter()
	w.Int(e.Size)
	w.Bytes(e.Digest)
	return w.Body()
}

func decodeEnd(body []byte) (End, error) {
	var out End

	r := wire.NewReader(body)
	size, err := r.Int()
	if err != nil {
		return out, err
	}
	digest, err := r.Bytes(64)
	if err != nil {
		return out, err
	}
	out.Size = size
	out.Digest = append([]byte(nil), digest...)
	return out, nil
}

// Ack is the receiver's verdict on one item, after hashing what arrived.
type Ack struct {
	OK     bool
	Reason string
}

func (a Ack) encode() []byte {
	w := wire.NewWriter()
	w.Bool(a.OK)
	w.String(a.Reason)
	return w.Body()
}

func decodeAck(body []byte) (Ack, error) {
	var out Ack

	r := wire.NewReader(body)
	ok, err := r.Bool()
	if err != nil {
		return out, err
	}
	reason, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	out.OK, out.Reason = ok, reason
	return out, nil
}

// Reject declines a session.
type Reject struct {
	Reason string
}

func (x Reject) encode() []byte {
	w := wire.NewWriter()
	w.String(x.Reason)
	return w.Body()
}

func decodeReject(body []byte) (Reject, error) {
	r := wire.NewReader(body)
	reason, err := r.String(wire.MaxString)
	return Reject{Reason: reason}, err
}

// Resize reports a new terminal size on a duplex session.
type Resize struct {
	Cols uint16
	Rows uint16
}

func (z Resize) encode() []byte {
	w := wire.NewWriter()
	w.Uint(uint64(z.Cols))
	w.Uint(uint64(z.Rows))
	return w.Body()
}

func decodeResize(body []byte) (Resize, error) {
	var out Resize

	r := wire.NewReader(body)
	cols, err := r.Uint()
	if err != nil {
		return out, err
	}
	rows, err := r.Uint()
	if err != nil {
		return out, err
	}
	out.Cols, out.Rows = uint16(cols), uint16(rows)
	return out, nil
}

// Declined is a far end that answered and said no.
//
// Worth telling apart from a far end that could not be reached: one is a device that is off, which
// is what a queue is for, and the other is an answer. Queueing an answer means retrying forever
// against a decision somebody made.
type Declined struct {
	Reason string
}

func (d Declined) Error() string { return "declined: " + d.Reason }

// WasDeclined reports whether an error is a refusal rather than a failure to reach anybody.
func WasDeclined(err error) bool {
	var declined Declined
	return errors.As(err, &declined)
}
