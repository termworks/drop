package proto

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Policy decides what a receiving node does with an incoming session.
type Policy struct {
	// Mounts is every namespace this node serves. Without it nothing is served.
	Mounts *ns.Table
	// Dir is where accepted files are written when a mount does not say.
	Dir string
	// Who describes a caller: what it is filed under, and whether a secret is shared with it.
	// Nil means nothing is known about anyone, which with deny-by-default serves nobody.
	Who func(from node.ID, badge Badged) ns.Caller
	// Allow decides whether to take a session. Nil accepts nothing.
	Allow func(from node.ID, open Open) (bool, string)
	// Progress, when set, is called as bytes land. Total is SizeUnknown for an item with no length.
	Progress func(name string, done, total int64)
	// Done, when set, is called once an item is complete and verified.
	Done func(from node.ID, name string, size int64)
	// Finished, when set, is called once every item in a files session has landed.
	Finished func(from node.ID, count int)
	// Message, when set, stores one arriving conversation message. Returning an error means it was
	// not stored, and the sender will send it again.
	Message func(from node.ID, m convo.Message) error
	// Duplex, when set, handles an accepted live stream. Without it, live streams are refused.
	Duplex func(at Resolved, d *Duplex) error
	// Refused, when set, is called when a caller is turned away. It is a note of who knocked, so
	// that letting a bare id in later does not mean copying it out of a log by hand.
	Refused func(from node.ID, asked, why string)
}

// Handle takes one session stream. The transport is not its concern: anything that reads and
// writes will do, which is what lets the endpoint underneath change without touching this.
func Handle(s Stream, from node.ID, policy Policy) error {
	conn := wire.NewConn(s)

	kind, body, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("reading the open from %s: %w", node.Brief(from), err)
	}
	if kind != wire.KindOpen {
		return fmt.Errorf("%s started with frame kind %d, expected an open", node.Brief(from), kind)
	}

	open, err := decodeOpen(body)
	if err != nil {
		return fmt.Errorf("reading the open from %s: %w", node.Brief(from), err)
	}

	for _, item := range open.Items {
		if safeName(item.Name) == "" {
			_ = conn.WriteFrame(wire.KindReject, Reject{Reason: "an offered name is not a file name"}.encode())
			return fmt.Errorf("%s offered an unusable name %q", node.Brief(from), item.Name)
		}
	}

	// The namespace decides what this session is, so it is resolved before anything else.
	caller := ns.Caller{ID: from.String()}
	if policy.Who != nil {
		caller = policy.Who(from, vouched(from, open))
	}
	caller.Password = open.Secret

	at, err := resolve(policy.Mounts, from, caller, open)
	if err != nil {
		turnedAway(policy, from, open.Path, err.Error())
		return conn.WriteFrame(wire.KindReject, Reject{Reason: err.Error()}.encode())
	}

	allowed, reason := false, "not accepting sessions"
	if policy.Allow != nil {
		allowed, reason = policy.Allow(from, open)
	}
	if !allowed {
		turnedAway(policy, from, open.Path, reason)
		return conn.WriteFrame(wire.KindReject, Reject{Reason: reason}.encode())
	}

	switch open.Mode {
	case ModeFiles:
		return receiveFiles(conn, withDir(policy, at.Mount.Dir), from, open)

	case ModeMessages:
		return receiveMessages(conn, policy, from)

	case ModeDuplex:
		if policy.Duplex == nil {
			return conn.WriteFrame(wire.KindReject, Reject{Reason: "not accepting live streams"}.encode())
		}
		if err := conn.WriteFrame(wire.KindAccept, Accept{}.encode()); err != nil {
			return err
		}
		return policy.Duplex(at, &Duplex{conn: conn, stream: s})

	default:
		return conn.WriteFrame(wire.KindReject, Reject{Reason: "unknown session mode"}.encode())
	}
}

// withDir points a policy at the directory its namespace declared.
func withDir(policy Policy, dir string) Policy {
	if dir != "" {
		policy.Dir = dir
	}
	return policy
}

// turnedAway notes a caller that was refused, when anybody is keeping that note.
func turnedAway(policy Policy, from node.ID, asked, why string) {
	if policy.Refused == nil {
		return
	}
	policy.Refused(from, asked, why)
}
