package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

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
func accepting(pinned *book.Book, any bool) func(node.ID, proto.Open) (bool, string) {
	return func(from node.ID, open proto.Open) (bool, string) { return true, "" }
}

// gather turns command line arguments into sources, with - meaning standard input.
func gather(args []string, stdinName string) ([]proto.Source, error) {
	var sources []proto.Source

	for _, path := range args {
		if path == "-" {
			sources = append(sources, proto.FileFromReader(stdinName, os.Stdin))
			continue
		}
		src, err := proto.FileFromPath(path)
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
func greeting(pinned *book.Book, mounts *ns.Table, from node.ID, badge proto.Badged) proto.Hello {
	// No pairing check here any more: the rules on the paths decide, and one of them may name a
	// bare key. What an unpaired caller can reach is usually nothing, and then the list is empty.
	return proto.Hello{
		Name:    node.DisplayName(),
		Version: version,
		Serves:  proto.Describe(mounts, whoIs(pinned)(from, badge)),
	}
}

// whoIs describes a caller from the address book, for the access rules to judge.
//
// Nothing here decides anything: it reports what is known — the name this device is filed under, and
// whether a secret is shared with it — and the rule on the path does the deciding.
// whoIs turns a caller into what the address book knows about it.
//
// There are two ways in. The machine itself may be in the book, which is how drop has always
// worked. Or it may carry a badge signed by a person who is -- a machine of theirs this one has
// never met, recognised without pairing again. Both may be true at once, and then the machine's
// own entry names it and the badge says whose it is.
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
		who.User = badge.Key

		// A machine of my own is filed under "me". Nobody writes it in the address book, because
		// there is nothing to pair with: it is whatever my own user key has signed.
		// A machine of my own is trusted by construction: it is me.
		if mine := myKey(); mine != "" && badge.Key == mine {
			who.UserName, who.Paired, who.Trusted = "me", true, true
			if who.Name == "" {
				who.Name = badge.As
			}
			return who
		}

		owner, known := pinned.ByUser(badge.Key)
		if !known {
			return who
		}
		who.UserName = owner.Person
		who.Paired = who.Paired || owner.Paired()
		who.Trusted = who.Trusted || owner.Trusted
		if who.Name == "" {
			who.Name = badge.As
		}
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
