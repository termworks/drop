package proto

import (
	"errors"
	"fmt"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Two machines of one user's handing each other what they know.
//
// The address book is one book across all of a user's machines: somebody paired from the phone is
// known to the laptop, a name changed on one is changed on all of them, and whatever is removed
// anywhere is removed everywhere. So each hands the other everything it holds, the other keeps
// whatever is newer, and answers with what it holds after that — one round trip, and both have
// the newer of everything. The ask carries the asker's badge the way a hello does, and the far end
// answers only a machine whose badge is its own user's.

// MaxSynced bounds what one machine hands another: a full address book and every mark.
const MaxSynced = 4 << 20

func encodeSync(state []byte) []byte {
	w := wire.NewWriter()
	w.Bytes(showable())
	w.Bytes(state)
	return w.Body()
}

func decodeSync(from node.ID, body []byte) (Badged, []byte, error) {
	r := wire.NewReader(body)
	shown, err := r.Bytes(wire.MaxString)
	if err != nil {
		return Badged{}, nil, err
	}
	who, _, _ := showing(from, shown)
	state, err := r.Bytes(MaxSynced)
	if err != nil {
		return Badged{}, nil, err
	}
	if !r.Done() {
		return Badged{}, nil, errors.New("a sync has trailing bytes")
	}
	return who, state, nil
}

// AskSync hands the far end what this machine holds, and returns what the far end holds after
// taking it.
func AskSync(s Stream, state []byte) ([]byte, error) {
	var out []byte
	c := wire.NewConn(s)
	err := c.WithIdle(settleIn, func() error {
		if err := c.WriteFrame(wire.KindPing, encodeSync(state)); err != nil {
			return fmt.Errorf("handing over: %w", err)
		}
		kind, body, err := c.ReadFrameUpTo(MaxSynced)
		if err != nil {
			return fmt.Errorf("reading what came back: %w", err)
		}
		switch kind {
		case wire.KindOpen:
			out = body
			return nil
		case wire.KindReject:
			reject, err := wire.DecodeReject(body)
			if err != nil {
				return err
			}
			return errors.New(reject.Reason)
		}
		return fmt.Errorf("reading what came back: frame kind %d", kind)
	})
	return out, err
}

// AnswerSync takes what a machine handed over and answers with whatever take says, or with why not.
func AnswerSync(s Stream, from node.ID, take func(Badged, []byte) ([]byte, error)) error {
	c := wire.NewConn(s)
	return c.WithIdle(settleIn, func() error {
		kind, body, err := c.ReadFrameUpTo(MaxSynced + wire.MaxString)
		if err != nil {
			return fmt.Errorf("reading what was handed over: %w", err)
		}
		if kind != wire.KindPing {
			return fmt.Errorf("reading what was handed over: expected frame kind %d, got %d", wire.KindPing, kind)
		}
		who, state, err := decodeSync(from, body)
		if err != nil {
			return err
		}
		answer, err := take(who, state)
		if err != nil {
			return c.WriteFrame(wire.KindReject, wire.Reject{Reason: err.Error()}.Encode())
		}
		return c.WriteFrame(wire.KindOpen, answer)
	})
}
