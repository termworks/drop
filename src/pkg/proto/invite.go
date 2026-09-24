package proto

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/plain"
	"github.com/bresilla/drop/src/pkg/wire"
)

// One device asking another, on the same network, to connect — and a person on the other one
// saying yes.
//
// Nobody types anything. The asker says what it wants — to make the other one of its user's
// machines, to pair with whoever owns it, or to become one of theirs — and, when it is the one
// showing a code, the code itself: the connection this travels on is already the two devices'
// own, so the code reaches only the device it was meant for. Both screens show the same short
// number worked out from the two ids, so the person saying yes can see it is this device asking
// and not another one next to it.

// What an invite asks for.
const (
	// InviteMine asks the far end to become one of the asker's user's machines.
	InviteMine = "mine"
	// InvitePair asks to pair with whoever owns the far end.
	InvitePair = "pair"
	// InviteJoin asks to become one of the far end's user's machines.
	InviteJoin = "join"
)

// Invite is one device asking another to connect.
type Invite struct {
	Kind string
	// Code is the one the asker is showing, for InviteMine and InvitePair: the far end takes it.
	Code string
	// Name is what the asker calls itself.
	Name string
}

// Reply is what the far end's person said.
type Reply struct {
	Yes bool
	// Code is the one the far end is showing when it said yes to InviteJoin: the asker takes it.
	Code string
	Why  string
}

// DecideWithin is how long an ask waits for a person on the far end.
const DecideWithin = 2 * time.Minute

// Check is the number both screens show for an invite: worked out from the device asked and from
// who is asking — their user key, so it is the same on every machine of theirs whichever of them
// does the asking, or the asking device when it wears no badge.
func Check(a, b string) string {
	lo, hi := a, b
	if hi < lo {
		lo, hi = hi, lo
	}
	sum := sha256.Sum256([]byte("drop ask check/1:" + lo + ":" + hi))
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(sum[:4])%1_000_000)
}

func (a Invite) encode() []byte {
	w := wire.NewWriter()
	w.Bytes(showable())
	w.String(a.Kind)
	w.String(a.Code)
	w.String(a.Name)
	return w.Body()
}

func decodeInvite(from node.ID, body []byte) (Badged, Invite, error) {
	var a Invite
	r := wire.NewReader(body)
	shown, err := r.Bytes(wire.MaxString)
	if err != nil {
		return Badged{}, a, err
	}
	who, _, _ := showing(from, shown)
	if a.Kind, err = r.String(16); err != nil {
		return Badged{}, a, err
	}
	if a.Code, err = r.String(64); err != nil {
		return Badged{}, a, err
	}
	if a.Name, err = r.String(256); err != nil {
		return Badged{}, a, err
	}
	if !r.Done() {
		return Badged{}, a, errors.New("an ask has trailing bytes")
	}
	switch a.Kind {
	case InviteMine, InvitePair, InviteJoin:
	default:
		return Badged{}, a, fmt.Errorf("%q is nothing a device is asked", a.Kind)
	}
	a.Name = plain.Line(a.Name)
	return who, a, nil
}

func (a Reply) encode() []byte {
	w := wire.NewWriter()
	w.Bool(a.Yes)
	w.String(a.Code)
	w.String(a.Why)
	return w.Body()
}

func decodeReply(body []byte) (Reply, error) {
	var a Reply
	r := wire.NewReader(body)
	var err error
	if a.Yes, err = r.Bool(); err != nil {
		return a, err
	}
	if a.Code, err = r.String(64); err != nil {
		return a, err
	}
	if a.Why, err = r.String(wire.MaxString); err != nil {
		return a, err
	}
	if !r.Done() {
		return a, errors.New("an answer has trailing bytes")
	}
	a.Why = plain.Line(a.Why)
	return a, nil
}

// SendInvite asks, and waits for the far end's person to answer.
func SendInvite(s Stream, a Invite) (Reply, error) {
	var out Reply
	c := wire.NewConn(s)
	err := c.WithIdle(DecideWithin+settleIn, func() error {
		if err := c.WriteFrame(wire.KindPing, a.encode()); err != nil {
			return fmt.Errorf("asking: %w", err)
		}
		kind, body, err := c.ReadFrameUpTo(wire.MaxString)
		if err != nil {
			return fmt.Errorf("waiting for an answer: %w", err)
		}
		if kind != wire.KindOpen {
			return fmt.Errorf("waiting for an answer: frame kind %d", kind)
		}
		out, err = decodeReply(body)
		return err
	})
	return out, err
}

// AnswerInvite reads an ask and answers it with whatever decide says, which may take as long as a
// person takes.
func AnswerInvite(s Stream, from node.ID, decide func(Badged, Invite) Reply) error {
	c := wire.NewConn(s)
	return c.WithIdle(DecideWithin+settleIn, func() error {
		kind, body, err := c.ReadFrameUpTo(MaxUnknown)
		if err != nil {
			return fmt.Errorf("reading the ask: %w", err)
		}
		if kind != wire.KindPing {
			return fmt.Errorf("reading the ask: expected frame kind %d, got %d", wire.KindPing, kind)
		}
		who, a, err := decodeInvite(from, body)
		if err != nil {
			return c.WriteFrame(wire.KindOpen, Reply{Why: err.Error()}.encode())
		}
		return c.WriteFrame(wire.KindOpen, decide(who, a).encode())
	})
}
