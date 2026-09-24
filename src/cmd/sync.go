package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/grant"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/shares"
	"github.com/bresilla/drop/src/pkg/user"
)

// One address book across every machine of this user's.
//
// Whoever you pair with from one machine is known to all of them, a name or a trust changed on one
// is changed on all of them, and whatever is removed anywhere is removed everywhere. Each machine
// keeps its own book, because what it holds is its own — the secrets it made, where it last saw
// everybody — and hands the rest of its machines everything in it. Every entry says when it was
// last decided about and every removal is a mark with a time, so of two words about one machine
// the newer is the one that stands, whichever machine it came from and in whichever order.

// synced is what one machine of this user's hands another.
type synced struct {
	Entries []syncedEntry        `json:"entries"`
	Marks   map[string]user.Mark `json:"marks"`
}

// syncedEntry is one machine as the book holds it. The secret goes only for somebody else's
// machine: between two machines of yours it is worked out from the circle, and differs per pair.
type syncedEntry struct {
	Name    string   `json:"name"`
	ID      string   `json:"id"`
	Secret  []byte   `json:"secret,omitempty"`
	Addrs   []string `json:"addrs,omitempty"`
	User    string   `json:"user,omitempty"`
	Person  string   `json:"person,omitempty"`
	Trusted bool     `json:"trusted,omitempty"`
	At      int64    `json:"at"`
}

// bookToHand is everything this machine hands another of its user's.
func bookToHand(pinned *book.Book) synced {
	out := synced{Marks: map[string]user.Mark{}}
	if held, err := user.Marks(); err == nil {
		out.Marks = held
	}
	for _, e := range pinned.All() {
		one := syncedEntry{Name: e.Name, ID: e.ID.String(), Addrs: e.Addrs, User: e.User, Person: e.Person, Trusted: e.Trusted, At: e.At}
		if e.User == "" || e.User != myKey() {
			one.Secret = e.Secret
		}
		out.Entries = append(out.Entries, one)
	}
	return out
}

// takeState keeps whatever another machine of this user's holds that is newer than what is held
// here, and forgets whatever it took out. It says whether anything changed.
func takeState(pinned *book.Book, theirs synced) (bool, error) {
	gone, err := user.Merge(theirs.Marks)
	if err != nil {
		return false, err
	}
	marks, err := user.Marks()
	if err != nil {
		return false, err
	}
	self, err := node.LocalID()
	if err != nil {
		return false, err
	}
	circle, _ := user.Circle()

	var renamed []renamedHere
	wroteAny := false
	err = pinned.Change(func() (bool, error) {
		wrote := false
		defer func() { wroteAny = wrote }()
		for _, at := range gone {
			if id, err := node.ParseID(at); err == nil {
				if entry, ok := pinned.ByID(id); ok {
					_ = shares.Forget(id)
					pinned.Remove(entry.Name)
					wrote = true
				}
			}
		}

		for _, e := range theirs.Entries {
			id, err := node.ParseID(e.ID)
			if err != nil || id == self {
				continue
			}
			if m, ok := marks[e.ID]; ok && m.Gone && m.At >= e.At {
				continue
			}
			held, known := pinned.ByID(id)
			if known && held.At >= e.At {
				continue
			}

			next := book.Entry{
				Name: freeName(pinned, fileName(e.Name, id), id), ID: id, Addrs: e.Addrs,
				User: e.User, Person: e.Person, Trusted: e.Trusted, At: e.At, Secret: e.Secret,
			}
			switch {
			case known && held.Paired() && !held.Circle:
				// A secret this machine made with it itself is the one the far end knows.
				next.Secret, next.Addrs = held.Secret, held.Addrs
			case e.User != "" && e.User == myKey():
				next.Secret, next.Circle = nil, true
				if len(circle) > 0 {
					next.Secret = user.PairSecret(circle, self.String(), e.ID)
				}
			}
			if known && len(held.Addrs) > 0 {
				next.Addrs = held.Addrs
			}
			if known && held.Name != next.Name {
				renamed = append(renamed, renamedHere{old: held.Name, name: next.Name, machine: true})
			}
			if known && held.Person != "" && held.Person != next.Person && next.Person != "" {
				renamed = append(renamed, renamedHere{old: held.Person, name: next.Person})
			}
			pinned.Put(next)
			wrote = true
		}
		return wrote, nil
	})
	if err != nil {
		return false, err
	}
	carryRenames(renamed)
	return wroteAny || len(gone) > 0, nil
}

// renamedHere is one name another machine of this user's changed, to carry into the grants here.
type renamedHere struct {
	old, name string
	machine   bool
}

// carryRenames gives every grant and holding that named somebody their new name.
func carryRenames(all []renamedHere) {
	if len(all) == 0 {
		return
	}
	store, err := grant.Load()
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, r := range all {
		if seen[r.old] {
			continue
		}
		seen[r.old] = true
		_ = store.Rename(r.old, r.name, r.machine)
		if !r.machine {
			_ = renameHolders(r.old, r.name)
		}
	}
}

// freeName is a name for a machine here: the one it came with, or that with a number after it when
// another machine already has it.
func freeName(pinned *book.Book, name string, id node.ID) string {
	at := name
	for i := 2; ; i++ {
		held, taken := pinned.Lookup(at)
		if !taken || held.ID == id {
			return at
		}
		at = fmt.Sprintf("%s-%d", name, i)
	}
}

// syncing answers another machine of this user's handing over what it holds.
func syncing(pinned *book.Book) func(node.ID, *iroh.Stream) {
	return func(from node.ID, s *iroh.Stream) {
		defer func() { _ = s.Close() }()
		if err := pinned.Refresh(); err != nil {
			return
		}
		_ = proto.AnswerSync(s, from, func(badge proto.Badged, raw []byte) ([]byte, error) {
			if who := whoIs(pinned)(from, badge, proto.Stood{}); who.UserName != ns.LevelMe {
				return nil, errNotMine
			}
			var theirs synced
			if err := json.Unmarshal(raw, &theirs); err != nil {
				return nil, err
			}
			if changed, err := takeState(pinned, theirs); err != nil {
				return nil, err
			} else if changed {
				nudgeMine()
			}
			return json.Marshal(bookToHand(pinned))
		})
	}
}

// syncWith hands one machine of this user's what this one holds, and keeps what comes back.
func syncWith(ctx context.Context, over reaches, pinned *book.Book, entry book.Entry) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	done, s, err := over.To(ctx, entry, node.ALPNSync)
	if err != nil {
		return err
	}
	defer func() { _ = done.Close() }()
	defer func() { _ = s.Close() }()
	defer stopStreamOnDone(ctx, s)()

	mine, err := json.Marshal(bookToHand(pinned))
	if err != nil {
		return err
	}
	raw, err := proto.AskSync(s, mine)
	if err != nil {
		return err
	}
	var theirs synced
	if err := json.Unmarshal(raw, &theirs); err != nil {
		return err
	}
	_, err = takeState(pinned, theirs)
	return err
}

// bookStamp is when this machine's address book and marks last changed, whoever changed them: a
// command in another process is as much a change as one made here.
func bookStamp() string {
	dir, err := node.ConfigDir()
	if err != nil {
		return ""
	}
	out := ""
	for _, name := range []string{"peers.json", "gone.json"} {
		if at, err := os.Stat(filepath.Join(dir, name)); err == nil {
			out += fmt.Sprintf("%s:%d:%d;", name, at.ModTime().UnixNano(), at.Size())
		}
	}
	return out
}
