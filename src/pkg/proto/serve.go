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
	// Met, when set, takes a catch-up on a namespace several machines hold. Nil says this node
	// keeps no history, which is what a process serving one namespace for one command does.
	Met func(m Meeting) error
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

	// A refusal a caller could do nothing about is answered as passing; one that is a decision
	// about them is answered as settled, so a sender with something queued knows which.
	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReject, wire.Reject{Reason: reason}.Encode())
	}
	decided := func(reason string) error {
		return conn.WriteFrame(wire.KindReject, wire.Reject{Reason: reason, Settled: true}.Encode())
	}

	caller := ns.Caller{ID: from.String()}
	if policy.Who != nil {
		caller = policy.Who(from, vouched(from, open))
	}

	// The path is cleaned here and nowhere else after: what a peer wrote is a thousand arbitrary
	// bytes, and it is answered, written down and drawn in an interface.
	path, err := ns.Clean(open.Path)
	if err != nil {
		turnedAway(policy, from, "", unreadable)
		return decided(unreadable)
	}
	open.Path = path

	// Asking is what somebody does *because* they are not admitted, so it is answered before the
	// access rules turn them away — but only for a path they are allowed to know exists. What the
	// frame carries here is what the caller said about the request, never a password, so nothing
	// on this path is hashed.
	if open.Ask {
		if policy.Mounts == nil || !policy.Mounts.Sees(path, caller) {
			return decided("no such path")
		}
		return TakeAsk(conn, policy, Asker{
			From:   from,
			Name:   caller.Name,
			Person: caller.UserName,
			Path:   path,
			Why:    open.Secret,
		})
	}

	// A caller that holds this namespace too is here to catch up rather than to open anything, so it
	// is answered before the archetype is looked at: what is said afterwards is heads and changes,
	// which no archetype would recognise. It names the namespace itself, so nothing below resolves a
	// path for it.
	if open.Meet {
		return meeting(conn, policy, open, caller, from, refuse, decided)
	}

	caller.Password = open.Secret
	if open.Secret != "" && !guessing.spare(from) {
		turnedAway(policy, from, path, "too many password attempts")
		return refuse("too many attempts, wait a while")
	}

	mount, rest, no := resolve(policy.Mounts, caller, path)
	if no != nil {
		told := no.told

		// A path somebody can see but not open is a door with a bell on it. Saying so is the
		// difference between "there is nothing here" and "you may ask for this". Whatever was
		// offered has already been judged by the rule, so it is left out of this one.
		asking := caller
		asking.Password = ""
		if policy.Mounts != nil && policy.Mounts.Sees(path, asking) {
			told, no.settled = fmt.Sprintf("%s: you may ask for it", path), true
		}

		turnedAway(policy, from, path, no.noted)
		if no.settled {
			return decided(told)
		}
		return refuse(told)
	}
	guessing.forget(from)

	allowed, reason := false, "not accepting sessions"
	if policy.Allow != nil {
		allowed, reason = policy.Allow(from, open)
	}
	if !allowed {
		turnedAway(policy, from, path, reason)
		return refuse(reason)
	}

	if policy.Archetypes == nil {
		return refuse("this node serves no namespace types")
	}
	answers, known := policy.Archetypes.Lookup(mount.Archetype, mount.Version)
	if !known {
		return refuse(policy.Archetypes.Missing(mount.Archetype, mount.Version).Error())
	}

	// The caller may say what it expects to find. An empty name asks for whatever is here, which is
	// what somebody who typed a path rather than read a listing is doing, and the archetype this
	// one speaks the protocol of is the same conversation under a name the caller has heard of.
	if open.Archetype != "" && open.Archetype != mount.Archetype && open.Archetype != answers.Note(mount.Config).Shape {
		return decided(fmt.Sprintf("%s is a %s namespace", mount.Path, mount.Archetype))
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

// unreadable is what a caller is told about a path this node cannot even spell.
const unreadable = "that is not a path this node can read"

// notHeld is what a caller is told about a namespace this node does not hold, and about one it
// holds and does not share with them.
//
// One answer for both, the same way a path somebody may not open reads as one that is not there.
// Which of the two it is is a fact about this machine, and answering a guess would draw the map.
const notHeld = "that is not a namespace held with you here"

// meeting answers a caller that holds the same namespace and wants to catch up.
//
// It is found by the name both machines work out for it rather than by any path: the path it has
// here is this machine's own word for it, the path over there is theirs, and a namespace taken up
// at another name would otherwise catch up with whatever happened to be spelled the same. Nothing
// under a namespace is meetable either, because a history is the whole of one thing.
func meeting(conn *wire.Conn, policy Policy, open Opening, caller ns.Caller, from node.ID, refuse, decided func(string) error) error {
	allowed, reason := false, "not accepting sessions"
	if policy.Allow != nil {
		allowed, reason = policy.Allow(from, open)
	}
	if !allowed {
		turnedAway(policy, from, open.Held, reason)
		return refuse(reason)
	}

	mount, found := holding(policy.Mounts, open.Held)
	if !found {
		turnedAway(policy, from, open.Held, "no namespace of that name is held here")
		return decided(notHeld)
	}
	// The rule on the mount, because whether an arriving change's author was allowed to make it is
	// judged by that same rule: the two halves of holding a thing together have to agree.
	if ok, why := policy.Mounts.Admits(mount.Path, caller); !ok {
		turnedAway(policy, from, mount.Path, fmt.Sprintf("%s: %s", mount.Path, why))
		return decided(notHeld)
	}
	if policy.Met == nil {
		return refuse("this node keeps no history")
	}

	if err := conn.WriteFrame(wire.KindAccept, nil); err != nil {
		return err
	}
	return policy.Met(Meeting{Mount: mount, Who: caller, From: from, Conn: conn})
}

// holding finds the namespace both machines call by one name.
func holding(table *ns.Table, name string) (ns.Mount, bool) {
	if table == nil || name == "" {
		return ns.Mount{}, false
	}
	for _, m := range table.All() {
		if m.Shared.Declared() && m.Shared.ID() == name {
			return m, true
		}
	}
	return ns.Mount{}, false
}

// refusal is a session that was not taken: what is written down here, and what the caller is told.
//
// The two are not the same. A caller who may not be here learns that and no more, because "nothing
// is mounted there" and "that is mine, not yours" are the two halves of a map of this machine, and
// answering a stranger's guesses draws it for them.
type refusal struct {
	noted string
	told  string
	// settled says this was a decision about the caller rather than something that may pass.
	settled bool
}

func (r *refusal) Error() string { return r.noted }

// resolve finds the namespace an open is asking for, and whether the caller may be in it.
func resolve(table *ns.Table, caller ns.Caller, path string) (ns.Mount, string, *refusal) {
	if table == nil {
		none := "this node serves no namespaces"
		return ns.Mount{}, "", &refusal{noted: none, told: none}
	}
	if path == "" {
		path = ns.Root
	}
	notYours := fmt.Sprintf("%s: not shared with you", path)

	mount, rest, found := table.Lookup(path)
	if !found {
		return ns.Mount{}, "", &refusal{noted: fmt.Sprintf("nothing is mounted at %s", path), told: notYours, settled: true}
	}
	// The tree decides, from the nearest rule above the path that was asked for. A branch with no
	// type still governs what is under it, which is the whole point of letting one exist, and a
	// grant written below a mount governs what is under that.
	if ok, why := table.Admits(path, caller); !ok {
		return ns.Mount{}, "", &refusal{noted: fmt.Sprintf("%s: %s", path, why), told: notYours, settled: true}
	}
	if mount.Branch() {
		held := fmt.Sprintf("%s holds other paths but serves nothing itself", mount.Path)
		return ns.Mount{}, "", &refusal{noted: held, told: held, settled: true}
	}
	return mount, rest, nil
}

// Meeting is one catch-up arriving: which namespace it is about, who is on the other end, and the
// stream it runs on.
//
// The access rule that governs the namespace comes off the mount, because deciding whether an
// arriving change's author was allowed to make it is the same question as deciding whether their
// machine could have opened the path.
type Meeting struct {
	Mount ns.Mount
	Who   ns.Caller
	From  node.ID
	Conn  *wire.Conn
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
