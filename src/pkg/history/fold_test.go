package history

import (
	"testing"
	"time"
)

// chain is a run of changes, each made after the one before it.
func chain(t *testing.T, l *Log, n int) []Change {
	t.Helper()

	var heads []ID
	out := make([]Change, 0, n)
	for i := range n {
		c := signed(t, string(rune('a'+i%26)), heads...)
		add(t, l, c)
		heads = []ID{c.ID()}
		out = append(out, c)
	}
	return out
}

// A history is a means, not a feature: once it is folded, what is left is what it came to.
func TestAFoldReplacesEverythingItStandsFor(t *testing.T) {
	asSomebody(t)

	l := aLog(t, thing)
	chain(t, l, 4)

	id, err := l.Fold([]byte("what it all came to"))
	if err != nil {
		t.Fatalf("Fold(): %v", err)
	}
	if held := read(t, l); !same(held, []string{"what it all came to"}) {
		t.Fatalf("Ordered() = %v, want just the snapshot", held)
	}
	if heads := l.Heads(); len(heads) != 1 || heads[0] != id {
		t.Fatalf("Heads() = %v, want just the snapshot", heads)
	}

	// And it is on the disk that way too, not only in this process.
	l.read = false
	if held := read(t, l); !same(held, []string{"what it all came to"}) {
		t.Fatalf("Ordered() from disk = %v", held)
	}
}

// A machine that never held the changes a snapshot stands for takes the snapshot on its own, which
// is what a peer coming back after a long time gets instead of the history it missed.
func TestAPeerWithNothingTakesASnapshotOnItsOwn(t *testing.T) {
	asSomebody(t)

	mine := aLog(t, thing)
	chain(t, mine, 4)
	if _, err := mine.Fold([]byte("what it all came to")); err != nil {
		t.Fatalf("Fold(): %v", err)
	}

	owed, err := mine.Since(nil)
	if err != nil {
		t.Fatalf("Since(): %v", err)
	}
	if len(owed) != 1 || !owed[0].Whole() {
		t.Fatalf("Since(nothing) = %v, want the snapshot alone", bodies(owed))
	}

	theirs := aLog(t, thing)
	add(t, theirs, owed...)
	if held := read(t, theirs); !same(held, []string{"what it all came to"}) {
		t.Fatalf("Ordered() = %v", held)
	}
}

// A peer that was caught up when the fold happened goes on changing the thing from where it was.
// What it names is gone from here, so the snapshot stands in its place and its change is placed
// after the snapshot rather than refused.
func TestAChangeMadeOnAHeadThatWasFoldedAwayIsStillTaken(t *testing.T) {
	asSomebody(t)

	l := aLog(t, thing)
	made := chain(t, l, 4)
	last := made[len(made)-1].ID()

	if _, err := l.Fold([]byte("what it all came to")); err != nil {
		t.Fatalf("Fold(): %v", err)
	}

	theirs := signed(t, "and then bob wrote this", last)
	if _, err := l.Add(theirs); err != nil {
		t.Fatalf("Add(): %v", err)
	}
	if held := read(t, l); !same(held, []string{"what it all came to", "and then bob wrote this"}) {
		t.Fatalf("Ordered() = %v", held)
	}
	if heads := l.Heads(); len(heads) != 1 || heads[0] != theirs.ID() {
		t.Fatalf("Heads() = %v, want just what bob wrote", heads)
	}
}

// Two machines that hold the same snapshot hold the same shape, whichever of them made it.
func TestAMachineThatTakesASnapshotForgetsWhatItReplaces(t *testing.T) {
	asSomebody(t)

	mine := aLog(t, thing)
	made := chain(t, mine, 4)

	theirs := aLog(t, thing)
	add(t, theirs, made...)

	whole, err := mine.Fold([]byte("what it all came to"))
	if err != nil {
		t.Fatalf("Fold(): %v", err)
	}
	owed, err := mine.Since(nil)
	if err != nil {
		t.Fatalf("Since(): %v", err)
	}
	add(t, theirs, owed...)

	if held := read(t, theirs); !same(held, []string{"what it all came to"}) {
		t.Fatalf("Ordered() = %v, want the four changes forgotten", held)
	}
	if heads := theirs.Heads(); len(heads) != 1 || heads[0] != whole {
		t.Fatalf("Heads() = %v, want just the snapshot", heads)
	}
}

// Nothing is folded while somebody who holds the thing is still behind: what they have not seen is
// what they are about to be sent.
func TestNothingIsFoldedWhileAPeerIsStillBehind(t *testing.T) {
	asSomebody(t)

	l := aLog(t, thing)
	made := chain(t, l, Least+1)

	if err := l.Seen("bob", []ID{made[0].ID()}); err != nil {
		t.Fatalf("Seen(): %v", err)
	}
	if l.Folding() {
		t.Fatal("a history was folded away with a peer still behind it")
	}

	if err := l.Seen("bob", l.Heads()); err != nil {
		t.Fatalf("Seen(): %v", err)
	}
	if !l.Folding() {
		t.Fatal("a history everybody has caught up on was not worth folding")
	}
}

// Below a certain size the snapshot costs more than the history it would replace.
func TestASmallHistoryIsNotWorthFolding(t *testing.T) {
	asSomebody(t)

	l := aLog(t, thing)
	chain(t, l, 3)
	if l.Folding() {
		t.Fatal("a history of three changes was worth folding")
	}
}

// A peer nobody has heard from in a long time is forgotten rather than waited for, because waiting
// for them is what makes a log that never stops growing.
func TestAPeerNobodyHasHeardFromIsForgotten(t *testing.T) {
	asSomebody(t)

	l := aLog(t, thing)
	made := chain(t, l, Least+1)

	long := time.Now().Add(-2 * Remember).UnixMilli()
	if err := l.remember([]far{{who: "bob", at: long, heads: []ID{made[0].ID()}}}); err != nil {
		t.Fatalf("remember(): %v", err)
	}

	kept, err := l.remembered()
	if err != nil {
		t.Fatalf("remembered(): %v", err)
	}
	if len(kept) != 0 {
		t.Fatalf("remembered() = %v, want nobody", kept)
	}
	if !l.Folding() {
		t.Fatal("a history was kept for a peer nobody has heard from")
	}
}
