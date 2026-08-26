package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bresilla/drop/src/pkg/among"
	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/arch/share"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/seen"
)

// accepting builds the policy that decides whose sessions to take. Pairing is the gate: a peer
// with no shared secret is refused whatever namespace it asks for.
// accepting is the coarse gate: is this node taking sessions at all.
//
// It no longer asks about pairing. Which paths a caller may reach is decided by the access rule
// on the path, and a rule may name a bare key or a password — neither of which involves being
// paired. Refusing here first would make those grants unreachable.
func accepting(pinned *book.Book, any bool) func(node.ID, proto.Opening) (bool, string) {
	return func(from node.ID, open proto.Opening) (bool, string) { return true, "" }
}

// gather turns command line arguments into sources, with - meaning standard input.
func gather(args []string, stdinName string) ([]share.Source, error) {
	var sources []share.Source

	for _, path := range args {
		if path == "-" {
			sources = append(sources, share.FileFromReader(stdinName, os.Stdin))
			continue
		}
		src, err := share.FileFromPath(path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}
	return sources, nil
}

// streamOver reports whether an error is just the far end going away, which is how every stream
// ends. Reporting that as a failure trains people to ignore the last line.
func streamOver(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || false {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, os.ErrClosed) {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "connection closed") || strings.Contains(text, "stream reset")
}

// greeting is what this node answers a hello with.
//
// The namespace list goes only to a peer that is paired: what a device serves says a great deal
// about it, and hello is answered by anyone who dials.
func greeting(pinned *book.Book, mounts *ns.Table, known *arch.Registry, from node.ID, badge proto.Badged) proto.Hello {
	// No pairing check here any more: the rules on the paths decide, and one of them may name a
	// bare key. What an unpaired caller can reach is usually nothing, and then the list is empty.
	serves := proto.Describe(mounts, known, whoIs(pinned)(from, badge))

	// Who else holds a shared namespace is not a list anybody keeps: it is the access rule read
	// against the address book, worked out here because that is where both of them are.
	for i, served := range serves {
		if !served.Shared.Declared() {
			continue
		}
		rule, found := mounts.AccessFor(served.Path)
		if !found {
			continue
		}
		serves[i].Holders = among.People(rule, pinned, myKey())
	}

	return proto.Hello{Name: node.DisplayName(), Version: version, Serves: serves}
}

// whoIs turns a caller into what the address book knows about it, for the access rules to judge.
//
// There are two ways in. The machine itself may be in the book, which is how drop has always
// worked. Or it may carry a badge signed by a person who is -- a machine of theirs this one has
// never met, recognised without pairing again. Both may be true at once, and then the machine's
// own entry names it and the badge says whose it is.
//
// Name is only ever what this machine filed the device under. The label on the badge is the far
// end's own word for itself, so it goes in Label, where nothing is decided on it: a rule written
// as "bob@laptop" has to mean the laptop somebody here wrote down, not whichever of bob's machines
// calls itself that.
func whoIs(pinned *book.Book) func(node.ID, proto.Badged) ns.Caller {
	return func(from node.ID, badge proto.Badged) ns.Caller {
		who := ns.Caller{ID: from.String()}

		if entry, ok := pinned.ByID(from); ok {
			who.Name = entry.Name
			who.Paired = entry.Paired()
			who.Trusted = entry.Trusted
		}

		if !badge.Shown() {
			return who
		}
		who.User, who.Label = badge.Key, badge.As

		// A machine of my own is filed under "me". Nobody writes it in the address book, because
		// there is nothing to pair with: it is whatever my own user key has signed.
		if mine := myKey(); mine != "" && badge.Key == mine {
			who.UserName, who.Paired, who.Trusted = "me", true, true
			return who
		}

		owner, known := pinned.ByUser(badge.Key)
		if !known {
			return who
		}
		who.UserName = owner.Person
		who.Paired = who.Paired || owner.Paired()
		who.Trusted = who.Trusted || owner.Trusted
		return who
	}
}

// noting writes down a caller that was turned away, unless it is somebody already known.
//
// A device in the address book being refused a path is an access rule doing its job, and there is
// nothing to look up afterwards. A stranger is the case this exists for: it dialled, so its id is
// known, and letting it in later should not mean copying sixty-four characters of hex out of a log.
func noting(pinned *book.Book) func(node.ID, string, string) {
	return func(from node.ID, asked, why string) {
		if _, known := pinned.ByID(from); known {
			return
		}
		_ = seen.Knocked(from, asked, why, time.Now())
	}
}
