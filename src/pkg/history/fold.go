package history

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/bresilla/drop/src/pkg/wire"
)

// Folding a history away.
//
// A history is a means, not a feature. Once everybody holding the thing has seen everything here,
// what happened stops being worth keeping and only what it came to is: one change carrying the
// whole state, naming the changes it stands in place of, signed and named by its bytes like any
// other. A machine that takes one forgets what it replaces; a machine that never held them takes
// it on its own and is caught up in one record.
//
// Who has caught up is what each peer said when it last met this one. A peer nobody has heard from
// in a long time is forgotten rather than waited for, because waiting for them is what makes a log
// that never stops growing. When they come back they are behind everything, so what they get is
// the snapshot rather than the history they missed.

// When a record is worth folding, and how long somebody is waited for.
const (
	// Remember is how long a peer that has not been heard from still counts.
	Remember = 30 * 24 * time.Hour
	// Least is how many changes make a fold worth making. Below it the snapshot costs more than
	// the history it replaces.
	Least = 1 << 6
)

// maxSeen caps how many peers are remembered, so a log's own bookkeeping is bounded too.
const maxSeen = 1 << 10

// far is one peer and how far they had got when they last said.
type far struct {
	who   string
	at    int64
	heads []ID
}

// Seen remembers what a peer said it held. It is what makes folding possible: a history can only
// be folded away once everybody still remembered has it.
func (l *Log) Seen(who string, heads []ID) error {
	if who == "" {
		return errors.New("remembering a peer: they have no name")
	}
	if len(heads) > MaxHeads {
		return fmt.Errorf("remembering %s: they named %d changes, over the %d limit", who, len(heads), MaxHeads)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	kept, err := l.remembered()
	if err != nil {
		return err
	}

	out := make([]far, 0, len(kept)+1)
	for _, p := range kept {
		if p.who != who {
			out = append(out, p)
		}
	}
	out = append(out, far{who: who, at: time.Now().UnixMilli(), heads: tidy(heads)})

	// Bounded by weight as well as by count, oldest word dropped first.
	//
	// Each peer is remembered along with every head it said it had, and a namespace being changed
	// many ways at once has a great many. Counting only the peers lets a few of them carry a file
	// that is rewritten and flushed on every meeting — so what one peer says costs every meeting
	// after it, for everybody.
	if len(out) > maxSeen {
		out = out[len(out)-maxSeen:]
	}
	for len(out) > 1 && weighed(out) > maxSeenHeads {
		out = out[1:]
	}
	return l.remember(out)
}

// weighed is how many heads are remembered in all.
func weighed(all []far) int {
	n := 0
	for _, p := range all {
		n += len(p.heads)
	}
	return n
}

// Folding reports whether the record is worth folding: there is enough of it to be worth replacing,
// and everybody still remembered has seen all of it.
func (l *Log) Folding() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.load(); err != nil || len(l.changes) < Least {
		return false
	}

	kept, err := l.remembered()
	if err != nil {
		return false
	}
	for _, p := range kept {
		if len(l.behind(p.heads)) != len(l.changes) {
			return false
		}
	}
	return true
}

// Fold replaces everything held with one change carrying what it all came to.
//
// The body is the archetype's: only it knows what its changes came to, and a snapshot that drop
// wrote itself would be a second answer to what a history means. What comes back is the id of the
// snapshot, which is now the only change here.
func (l *Log) Fold(body []byte) (ID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.load(); err != nil {
		return ID{}, err
	}
	if len(l.changes) == 0 {
		return ID{}, fmt.Errorf("folding %s: there is nothing here to fold", brief(l.at))
	}

	heads := make([]ID, 0, len(l.tips))
	for id := range l.tips {
		heads = append(heads, id)
	}
	cover := make([]ID, 0, len(l.changes))
	for id := range l.changes {
		cover = append(cover, id)
	}

	c, err := sign(l.at, body, heads, cover)
	if err != nil {
		return ID{}, fmt.Errorf("folding %s: %w", brief(l.at), err)
	}
	id, err := l.take(c)
	if err != nil {
		return ID{}, fmt.Errorf("folding %s: %w", brief(l.at), err)
	}
	return id, nil
}

// fold drops what a snapshot stands in place of and writes the log back without it.
//
// Everything is dropped, not only what this machine was holding when the snapshot was made: a
// snapshot taken from a peer replaces the same changes here that it replaced there, so two machines
// that hold it hold the same shape whichever of them made it.
func (l *Log) fold(c Change) error {
	for _, was := range c.Fold {
		if was != c.ID() {
			delete(l.changes, was)
		}
	}
	l.index()
	return l.rewrite()
}

// remembered is every peer still counted, oldest word first. One nobody has heard from in a long
// time is dropped here rather than waited for.
func (l *Log) remembered() ([]far, error) {
	raw, err := os.ReadFile(l.seen)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", l.seen, err)
	}

	r := wire.NewReader(raw)
	count, err := r.Uint()
	if err != nil || count > maxSeen {
		return nil, nil
	}

	stale := time.Now().Add(-Remember).UnixMilli()
	out := make([]far, 0, count)
	for range count {
		who, err := r.String(wire.MaxString)
		if err != nil {
			return out, nil
		}
		at, err := r.Int()
		if err != nil {
			return out, nil
		}
		heads, err := run(r, MaxHeads)
		if err != nil {
			return out, nil
		}
		if at >= stale {
			out = append(out, far{who: who, at: at, heads: heads})
		}
	}
	return out, nil
}

// remember writes that back, through a temporary file and a rename so an interruption leaves the
// old list or the new one.
func (l *Log) remember(all []far) error {
	w := wire.NewWriter()
	w.Uint(uint64(len(all)))
	for _, p := range all {
		w.String(p.who)
		w.Int(p.at)
		w.Uint(uint64(len(p.heads)))
		for _, head := range p.heads {
			w.Bytes(head[:])
		}
	}

	scratch := l.seen + ".new"
	if err := os.WriteFile(scratch, w.Body(), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", scratch, err)
	}
	if err := os.Rename(scratch, l.seen); err != nil {
		return fmt.Errorf("replacing %s: %w", l.seen, err)
	}
	return nil
}

// maxSeenHeads bounds how many heads are remembered across every peer put together. A peer that
// says it holds a great many is a peer whose word costs every meeting after it, for everybody.
const maxSeenHeads = 1 << 16
