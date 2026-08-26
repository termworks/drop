package proto

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Stream is what a session runs over: a bidirectional byte stream whose read side can be given a
// deadline, and whose write side can be closed on its own.
type Stream interface {
	io.ReadWriteCloser
	SetReadDeadline(t time.Time) error
}

// Policy decides what a receiving node does with an incoming session.
//
// Nothing here is about one kind of namespace. What happens once a session is accepted belongs to
// the archetype that was registered for it, and what that archetype needs from this process was
// given to it when it was built.
type Policy struct {
	// Mounts is every namespace this node serves. Without it nothing is served.
	Mounts *ns.Table
	// Archetypes is what those namespaces mean. Without it nothing can be answered.
	Archetypes *arch.Registry
	// Who describes a caller: what it is filed under, and whether a secret is shared with it.
	// Nil means nothing is known about anyone, which with deny-by-default serves nobody.
	Who func(from node.ID, badge Badged) ns.Caller
	// Allow decides whether to take a session. Nil accepts nothing.
	Allow func(from node.ID, open Opening) (bool, string)
	// Asked, when set, takes a request to reach a path the caller can see but not open. Returning
	// an error refuses the request; nothing here grants anything.
	Asked func(who Asker) error
	// Refused, when set, is called when a caller is turned away. It is a note of who knocked, so
	// that letting a bare id in later does not mean copying it out of a log by hand.
	Refused func(from node.ID, asked, why string)
}

// settleIn bounds the opening handshake. A peer that sends half a frame header and then nothing
// otherwise holds a goroutine and its buffer for as long as it likes.
const settleIn = 10 * time.Second

// Handle takes one session stream: it reads the open, decides whether the caller may be here, and
// hands what is left of the stream to whichever archetype the path belongs to.
//
// The transport is not its concern: anything that reads and writes will do, which is what lets the
// endpoint underneath change without touching this.
func Handle(ctx context.Context, s Stream, from node.ID, policy Policy) error {
	conn := wire.NewConn(s)

	_ = s.SetReadDeadline(time.Now().Add(settleIn))

	kind, body, err := conn.ReadFrame()
	if err != nil {
		// A stream opened and closed without a word is a peer that changed its mind, not a fault.
		if wire.Closed(err) {
			return nil
		}
		return fmt.Errorf("reading the open from %s: %w", node.Brief(from), err)
	}
	if kind != wire.KindOpen {
		return fmt.Errorf("%s started with frame kind %d, expected an open", node.Brief(from), kind)
	}

	open, err := decodeOpen(body)
	if err != nil {
		return fmt.Errorf("reading the open from %s: %w", node.Brief(from), err)
	}

	// The session is settled, and what it does next takes as long as it takes.
	_ = s.SetReadDeadline(time.Time{})

	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReject, wire.Reject{Reason: reason}.Encode())
	}

	caller := ns.Caller{ID: from.String()}
	if policy.Who != nil {
		caller = policy.Who(from, vouched(from, open))
	}
	caller.Password = open.Secret

	// Asking is what somebody does *because* they are not admitted, so it is answered before the
	// access rules turn them away — but only for a path they are allowed to know exists.
	if open.Ask {
		if policy.Mounts == nil || !policy.Mounts.Sees(open.Path, caller) {
			return refuse("no such path")
		}
		return TakeAsk(conn, policy, Asker{
			From:   from,
			Name:   caller.Name,
			Person: caller.UserName,
			Path:   open.Path,
			Why:    open.Secret,
		})
	}

	mount, rest, err := resolve(policy.Mounts, caller, open.Path)
	if err != nil {
		reason := err.Error()

		// A path somebody can see but not open is a door with a bell on it. Saying so is the
		// difference between "there is nothing here" and "you may ask for this".
		if policy.Mounts != nil && policy.Mounts.Sees(open.Path, caller) {
			reason = fmt.Sprintf("%s: you may ask for it", open.Path)
		}

		turnedAway(policy, from, open.Path, reason)
		return refuse(reason)
	}

	allowed, reason := false, "not accepting sessions"
	if policy.Allow != nil {
		allowed, reason = policy.Allow(from, open)
	}
	if !allowed {
		turnedAway(policy, from, open.Path, reason)
		return refuse(reason)
	}

	// The caller may say what it expects to find. An empty name asks for whatever is here, which is
	// what somebody who typed a path rather than read a listing is doing.
	if open.Archetype != "" && open.Archetype != mount.Archetype {
		return refuse(fmt.Sprintf("%s is a %s namespace", mount.Path, mount.Archetype))
	}
	if policy.Archetypes == nil {
		return refuse("this node serves no namespace types")
	}
	answers, known := policy.Archetypes.Lookup(mount.Archetype, mount.Version)
	if !known {
		return refuse(policy.Archetypes.Missing(mount.Archetype, mount.Version).Error())
	}

	if err := conn.WriteFrame(wire.KindAccept, nil); err != nil {
		return err
	}
	return answers.Serve(ctx, arch.Session{
		Path:   mount.Path,
		Rest:   rest,
		Config: mount.Config,
		From:   from,
		Who:    caller,
		Conn:   conn,
		Stream: s,
	})
}

// resolve finds the namespace an open is asking for, and whether the caller may be in it.
func resolve(table *ns.Table, caller ns.Caller, path string) (ns.Mount, string, error) {
	if table == nil {
		return ns.Mount{}, "", fmt.Errorf("this node serves no namespaces")
	}
	if path == "" {
		path = ns.Root
	}

	mount, rest, found := table.Lookup(path)
	if !found {
		return ns.Mount{}, "", fmt.Errorf("nothing is mounted at %s", path)
	}
	if mount.Branch() {
		return ns.Mount{}, "", fmt.Errorf("%s holds other paths but serves nothing itself", mount.Path)
	}
	// The tree decides, from the nearest rule above this path. A branch with no type still governs
	// what is under it, which is the whole point of letting one exist.
	if ok, why := table.Admits(mount.Path, caller); !ok {
		return ns.Mount{}, "", fmt.Errorf("%s: %s", mount.Path, why)
	}
	return mount, rest, nil
}

// Asker is somebody ringing the bell on a path they can see but cannot open.
type Asker struct {
	From node.ID
	// Who they are, as far as this machine knows: what the device is filed under, and the person
	// whose badge it carried.
	Name   string
	Person string
	Path   string
	// Why is what they said about it, and may be empty.
	Why string
}

// turnedAway notes a caller that was refused, when anybody is keeping that note.
func turnedAway(policy Policy, from node.ID, asked, why string) {
	if policy.Refused == nil {
		return
	}
	policy.Refused(from, asked, why)
}
