package proto

import (
	"errors"
	"fmt"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Asking a machine of your own who may reach its paths, and changing that.
//
// The machines of one person look after each other: a phone of yours can put a path on your
// computer on a step, let somebody into it, or keep somebody out. The ask carries the asker's badge
// the way a hello does, and the far end answers only a machine whose badge is its own user's.

// What a Manage asks for.
const (
	ManageList  = "list"
	ManageRead  = "read"
	ManageLevel = "level"
	ManageShown = "shown"
	ManageAllow = "allow"
	ManageDeny  = "deny"
	ManageUnset = "unset"
	// ManageFor asks what one person may open, with Who their user key, or the id of a machine
	// that belongs to nobody.
	ManageFor = "for"
	// ManageInvite asks the machine to invite a device, Who its id and Level what to ask, for a
	// machine of the same user's that cannot do it itself.
	ManageInvite = "invite"
	// ManageAdd puts a topic up on the machine and writes it down, Path where, Level who may open
	// it and Body what it is; ManageRemove takes one down.
	ManageAdd    = "add"
	ManageRemove = "remove"
)

// Manage is one ask. Level is the step to put the path on, empty to hand it back to its config;
// Shown, for ManageShown, is whether others may see it.
type Manage struct {
	Op    string
	Path  string
	Who   string
	Level string
	Shown bool
	// Body is what a topic being added is, as JSON. Written only when there is one, so an ask to a
	// machine that predates it reads as it always did.
	Body string
}

// MaxManaged bounds an answer: every path a machine serves, and who is let in and kept out of each.
const MaxManaged = 1 << 20

func (m Manage) encode() []byte {
	w := wire.NewWriter()
	w.Bytes(showable())
	w.String(m.Op)
	w.String(m.Path)
	w.String(m.Who)
	w.String(m.Level)
	w.Bool(m.Shown)
	if m.Body != "" {
		w.String(m.Body)
	}
	return w.Body()
}

func decodeManage(from node.ID, body []byte) (Badged, Manage, error) {
	var m Manage
	r := wire.NewReader(body)
	shown, err := r.Bytes(wire.MaxString)
	if err != nil {
		return Badged{}, m, err
	}
	who, _, _ := showing(from, shown)

	for _, field := range []*string{&m.Op, &m.Path, &m.Who, &m.Level} {
		if *field, err = r.String(wire.MaxString); err != nil {
			return Badged{}, m, err
		}
	}
	if m.Shown, err = r.Bool(); err != nil {
		return Badged{}, m, err
	}
	if !r.Done() {
		if m.Body, err = r.String(MaxManaged); err != nil {
			return Badged{}, m, err
		}
		if m.Body == "" {
			return Badged{}, m, errors.New("a manage ask has trailing bytes")
		}
	}
	if !r.Done() {
		return Badged{}, m, errors.New("a manage ask has trailing bytes")
	}
	return who, m, nil
}

// AskManage asks, and hands back the answer as the far end wrote it.
func AskManage(s Stream, m Manage) ([]byte, error) { return AskManageWithin(s, m, settleIn) }

// AskManageWithin asks, waiting as long as within for the answer: an invite is answered by a person.
func AskManageWithin(s Stream, m Manage, within time.Duration) ([]byte, error) {
	var out []byte
	c := wire.NewConn(s)
	err := c.WithIdle(within, func() error {
		if err := c.WriteFrame(wire.KindPing, m.encode()); err != nil {
			return fmt.Errorf("asking: %w", err)
		}
		kind, body, err := c.ReadFrameUpTo(MaxManaged)
		if err != nil {
			return fmt.Errorf("reading the answer: %w", err)
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
		return fmt.Errorf("reading the answer: frame kind %d", kind)
	})
	return out, err
}

// AnswerManage reads an ask and answers it with whatever do says, or with why not.
func AnswerManage(s Stream, from node.ID, do func(Badged, Manage) ([]byte, error)) error {
	c := wire.NewConn(s)
	return c.WithIdle(settleIn, func() error {
		kind, body, err := c.ReadFrameUpTo(MaxUnknown)
		if err != nil {
			return fmt.Errorf("reading the ask: %w", err)
		}
		if kind != wire.KindPing {
			return fmt.Errorf("reading the ask: expected frame kind %d, got %d", wire.KindPing, kind)
		}
		who, m, err := decodeManage(from, body)
		if err != nil {
			return err
		}

		answer, err := do(who, m)
		if err != nil {
			reject := wire.Reject{Reason: err.Error()}
			return c.WriteFrame(wire.KindReject, reject.Encode())
		}
		return c.WriteFrame(wire.KindOpen, answer)
	})
}

// TopicBody is what adding a topic carries: its kind, and the command when the kind takes one.
type TopicBody struct {
	Kind    string `json:"kind"`
	Command string `json:"command,omitempty"`
}
