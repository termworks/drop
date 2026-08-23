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
	at, err := resolve(policy.Mounts, from, open)
	if err != nil {
		return conn.WriteFrame(wire.KindReject, Reject{Reason: err.Error()}.encode())
	}

	allowed, reason := false, "not accepting sessions"
	if policy.Allow != nil {
		allowed, reason = policy.Allow(from, open)
	}
	if !allowed {
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
