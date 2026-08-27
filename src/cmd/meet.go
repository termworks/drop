package cmd

import (
	"context"
	"fmt"
	"sync"

	"github.com/bresilla/drop/src/pkg/among"
	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/meet"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Keeping a namespace several machines hold level with the machines that hold it.
//
// The exchange itself is somebody else's: this is when it runs and who it runs against. It runs on
// a connection arriving, on a change being made here, and on the same timer that empties a queue —
// three moments rather than one, because running it when there is nothing to say costs a few ids.

// meeting answers a catch-up somebody else opened.
//
// Whoever else holds this namespace is told about anything that was taken, so a change reaches a
// machine this one never dialled: it arrives here from the peer that had it, and goes on from here
// to the peers that do not. It stops of its own accord, because a machine that took nothing new has
// nothing to pass on.
func meeting(mounts *ns.Table, pinned *book.Book, told arch.Changed) func(proto.Meeting) error {
	return func(m proto.Meeting) error {
		l, err := history.Open(m.Mount.Shared.ID())
		if err != nil {
			return err
		}

		_ = pinned.Refresh()
		rule, _ := mounts.AccessFor(m.Mount.Path)

		caught, err := meet.Answer(m.Conn, l, whoMet(m), among.Admits(rule, pinned, myKey()))
		if caught.Taken > 0 && told != nil {
			told(m.Mount.Path)
		}
		if caught.Refused > 0 {
			trace(fmt.Sprintf("%s sent %d changes for %s from somebody it is not shared with",
				node.Brief(m.From), caught.Refused, m.Mount.Path))
		}
		return err
	}
}

// whoMet is what to remember a meeting's far end under: what this machine files their device as,
// and the device itself when it is nobody this machine has written down.
func whoMet(m proto.Meeting) string {
	if m.Who.Name != "" {
		return m.Who.Name
	}
	return m.From.String()
}

// told is what a change here does: it goes out to whoever else holds the namespace it happened in.
//
// This is what an archetype is handed so it can say something moved. It answers at once and does
// the reaching on a goroutine, because whatever made the change is in the middle of doing something
// else and a peer that cannot be reached takes as long to fail as it takes.
//
// One round at a time per namespace. A change arriving while a round is running is not a second
// round, it is the same round done again after this one, because what a round says is whatever the
// history holds when it runs — so a peer dribbling changes out one at a time gets one reaching-out
// rather than one per change.
func told(ctx context.Context, over reaches, mounts *ns.Table, pinned *book.Book) arch.Changed {
	var (
		mu      sync.Mutex
		running = map[string]bool{}
		again   = map[string]bool{}
	)

	return func(path string) {
		mount, _, ok := mounts.Lookup(path)
		if !ok || !mount.Shared.Declared() {
			return
		}

		at := mount.Path

		mu.Lock()
		if running[at] {
			again[at] = true
			mu.Unlock()
			return
		}
		running[at] = true
		mu.Unlock()

		go func() {
			for {
				reaching(ctx, over, at, mounts, pinned)

				mu.Lock()
				if !again[at] {
					delete(running, at)
					mu.Unlock()
					return
				}
				delete(again, at)
				mu.Unlock()
			}
		}()
	}
}

// reaching opens a meeting about one namespace with everyone the rule says holds it.
//
// The namespace is read off the table again rather than carried in, because a round may be the
// second one and a path taken down in between is not a path to reach anybody about.
func reaching(ctx context.Context, over reaches, at string, mounts *ns.Table, pinned *book.Book) {
	mount, _, ok := mounts.Lookup(at)
	if !ok || mount.Path != at || !mount.Shared.Declared() {
		return
	}
	_ = pinned.Refresh()

	rule, _ := mounts.AccessFor(at)
	for _, entry := range among.Holders(rule, pinned) {
		if _, err := catchUp(ctx, over, entry, mount, rule, pinned); err != nil {
			trace(fmt.Sprintf("telling %s about %s: %v", entry.Name, at, err))
		}
	}
}

// pushTo is everything this machine has to say to a peer without being asked: whatever is queued
// for them, and a catch-up on every namespace they hold with this one.
//
// One place rather than one per kind of thing to say. A device that comes back after a week needs
// its messages and its shared namespaces, and a second loop beside this one would be a second
// answer to the question of when a peer is worth speaking to.
func pushTo(ctx context.Context, over reaches, entry book.Entry, mounts *ns.Table, pinned *book.Book) {
	if _, err := deliverOver(ctx, over, entry, ChatPath, "chat"); err != nil {
		trace(fmt.Sprintf("pushing to %s: %v", entry.Name, err))
	}

	for _, mount := range mounts.All() {
		if !mount.Shared.Declared() {
			continue
		}
		rule, found := mounts.AccessFor(mount.Path)
		if !found || !among.Holds(rule, entry) {
			continue
		}
		if _, err := catchUp(ctx, over, entry, mount, rule, pinned); err != nil {
			trace(fmt.Sprintf("catching %s up on %s: %v", entry.Name, mount.Path, err))
		}
	}
}

// catchUp opens a meeting with one peer about one namespace.
func catchUp(ctx context.Context, over reaches, entry book.Entry, mount ns.Mount, rule ns.Access, pinned *book.Book) (meet.Caught, error) {
	l, err := history.Open(mount.Shared.ID())
	if err != nil {
		return meet.Caught{}, err
	}

	done, s, err := over.To(ctx, entry, node.ALPNSession)
	if err != nil {
		return meet.Caught{}, err
	}
	defer done.Close()
	defer s.Close()

	conn, err := proto.Meet(s, mount.Shared, node.DisplayName())
	if err != nil {
		return meet.Caught{}, err
	}
	return meet.Ask(conn, l, entry.Name, among.Admits(rule, pinned, myKey()))
}
