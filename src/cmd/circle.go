package cmd

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/shares"
	"github.com/bresilla/drop/src/pkg/user"
)

// Every machine of one user's, finding the rest through whichever of them it met first.
//
// Pairing a phone with one computer of yours makes it one of your machines everywhere — the badge
// says so to all of them — but it only knows the one it paired with. So your machines tell each
// other, on every hello, about the rest: which machines are yours, and the secret they all share
// and work each pair's secret out from. A machine named that way is written down and found the same
// way a paired one is, without anybody pairing the two.

// circleFor is what a machine of this user's is told about the rest of them: the secret they all
// share, made here if none of them has made one yet, and every machine of this user's this one
// knows, itself among them.
func circleFor(pinned *book.Book, to node.ID) ([]byte, []proto.Member) {
	secret, err := user.MakeCircle()
	if err != nil {
		return nil, nil
	}
	var mine []proto.Member
	if self, err := node.LocalID(); err == nil {
		mine = append(mine, proto.Member{ID: self.String(), Name: node.DisplayName()})
	}
	for _, entry := range pinned.All() {
		if entry.User != "" && entry.User == myKey() && entry.ID != to && !user.Removed(entry.ID.String()) {
			mine = append(mine, proto.Member{ID: entry.ID.String(), Name: entry.Name})
		}
	}
	return secret, mine
}

// marksFor is every machine of this user's taken out or put back, newest first, as a hello carries
// them.
func marksFor() []proto.Mark {
	held, err := user.Marks()
	if err != nil {
		return nil
	}
	out := make([]proto.Mark, 0, len(held))
	for id, m := range held {
		out = append(out, proto.Mark{ID: id, At: m.At, Gone: m.Gone})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	if len(out) > proto.MaxMarks {
		out = out[:proto.MaxMarks]
	}
	return out
}

// takeMarks keeps the marks another machine of this user's holds, and forgets here every machine
// they newly take out.
func takeMarks(marked []proto.Mark) {
	if len(marked) == 0 {
		return
	}
	theirs := make(map[string]user.Mark, len(marked))
	for _, m := range marked {
		theirs[m.ID] = user.Mark{At: m.At, Gone: m.Gone}
	}
	gone, err := user.Merge(theirs)
	if err != nil || len(gone) == 0 {
		return
	}
	pinned, err := book.Load()
	if err != nil {
		return
	}
	_ = pinned.Change(func() (bool, error) {
		wrote := false
		for _, at := range gone {
			id, err := node.ParseID(at)
			if err != nil {
				continue
			}
			if entry, ok := pinned.ByID(id); ok {
				_ = shares.Forget(id)
				pinned.Remove(entry.Name)
				wrote = true
			}
		}
		return wrote, nil
	})
}

// markRemoved takes a machine out, whoever's it is: marked, so every machine of this user's forgets
// it too and turns it away, and none of them writes it back in.
func markRemoved(entry book.Entry) error {
	if self, err := node.LocalID(); err == nil && entry.ID == self {
		return errors.New("that is this machine: take it out from another one of yours")
	}
	if err := user.Remove(entry.ID.String(), time.Now()); err != nil {
		return err
	}
	nudgeMine()
	return nil
}

// joinCircle writes down the machines of this user's that another one named, each under the secret
// the circle gives that pair. Taken only from a machine already known here to be this user's: a
// stranger's list of who is yours is not something to believe.
func joinCircle(from book.Entry, hello proto.Hello) {
	if len(hello.Circle) == 0 || from.User == "" || from.User != myKey() {
		return
	}
	takeMarks(hello.Gone)
	changed, err := user.AdoptCircle(hello.Circle)
	if err != nil {
		return
	}
	circle, err := user.Circle()
	if err != nil || len(circle) == 0 {
		return
	}
	self, err := node.LocalID()
	if err != nil {
		return
	}
	pinned, err := book.Load()
	if err != nil {
		return
	}

	_ = pinned.Change(func() (bool, error) {
		wrote := false
		// A circle that changed under its machines changes every pair's secret with it.
		if changed {
			for _, entry := range pinned.All() {
				if entry.Circle {
					pinned.Resecret(entry.Name, user.PairSecret(circle, self.String(), entry.ID.String()))
					wrote = true
				}
			}
		}
		for _, m := range hello.Mine {
			id, err := node.ParseID(m.ID)
			if err != nil || id == self || user.Removed(m.ID) {
				continue
			}
			if _, known := pinned.ByID(id); known {
				continue
			}
			pinned.Join(fileName(m.Name, id), id, user.PairSecret(circle, self.String(), m.ID), myKey())
			wrote = true
		}
		return wrote, nil
	})
}

// fileName is what a machine another one named is filed under here: its name, as a word a command
// line can take, or the start of its id when it has none worth using.
func fileName(name string, id node.ID) string {
	var out strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out.WriteRune(r)
		case r == ' ':
			out.WriteRune('-')
		}
	}
	if out.Len() == 0 {
		return node.Brief(id)
	}
	return out.String()
}

// mineEvery is how often this machine says hello to the other machines of its user's: often enough
// that a machine added to one is known to the rest within minutes, rarely enough to cost nothing.
const mineEvery = 5 * time.Minute

// mineNudge asks for a round now rather than at the next tick: a pairing that just landed is the
// moment a machine has something new to hear about.
var mineNudge = make(chan struct{}, 1)

func nudgeMine() {
	select {
	case mineNudge <- struct{}{}:
	default:
	}
}

// keepMine says hello to every machine of this user's this one knows, on a slow tick and whenever
// nudged. What comes back is the rest of them, and a fresh badge for a machine that cannot sign its
// own when that one is running low.
func keepMine(ctx context.Context, held *dial.Kept) {
	tick := time.NewTicker(mineEvery)
	defer tick.Stop()

	select {
	case <-ctx.Done():
		return
	case <-time.After(20 * time.Second):
	}
	// The book is watched as well as nudged: a command run in another process changes it too, and
	// what it changed has to reach the rest of this user's machines as quickly as a change made here.
	watch := time.NewTicker(watchEvery)
	defer watch.Stop()

	for {
		if pinned, err := book.Load(); err == nil {
			for _, entry := range pinned.All() {
				if entry.User == "" || entry.User != myKey() || user.Removed(entry.ID.String()) {
					continue
				}
				if hello, ok := askedHello(ctx, kept{held: held}, entry); ok {
					joinCircle(entry, hello)
					_ = syncWith(ctx, kept{held: held}, pinned, entry)
				}
			}
		}
		seen := bookStamp()
	wait:
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				break wait
			case <-mineNudge:
				break wait
			case <-watch.C:
				if bookStamp() != seen {
					break wait
				}
			}
		}
	}
}

// watchEvery is how often the book is looked at for a change made by another process.
const watchEvery = 3 * time.Second
