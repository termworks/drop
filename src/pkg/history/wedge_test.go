package history

import (
	"fmt"
	"testing"
)

// Can somebody the rule admits make a namespace unreadable for good?
//
// Ordering refuses a history whose changes name each other in a circle, and everything that reads
// a shared thing reads it through Ordering. What is written down stays written down, so a circle
// that got in would come back after a restart and never go away: not a refused connection, a
// namespace that is finished.
//
// A fold is where to look, because it is the one thing that changes what a change is placed behind
// after it has already been stored.
func TestNobodyCanMakeAHistoryUnreadable(t *testing.T) {
	asSomebody(t)
	l := aLog(t, thing)

	// An ordinary change, and one after it.
	h := added(t, l, "one", nil)
	x := added(t, l, "two", []ID{h})

	// A fold covering h, as a peer that folded would send.
	p := folded(t, l, "all of it", []ID{h, x}, []ID{h})

	// And a second fold covering the same ground, which is what two machines folding at once
	// would produce.
	q := folded(t, l, "all of it, again", []ID{h, x}, []ID{h})

	// h again, after both folds claimed to have replaced it.
	_ = addedMaybe(l, "one", nil)

	// And a change naming both folds, which is what a machine that saw both would sign.
	_ = addedMaybe(l, "after both", []ID{p, q})

	if _, err := l.Ordered(); err != nil {
		t.Fatalf("a peer made this history unreadable: %v", err)
	}

	// And again from the disk, because what is written down is what comes back.
	again, err := Open(l.at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Ordered(); err != nil {
		t.Fatalf("a history that reads here does not read after a restart: %v", err)
	}
}

func added(t *testing.T, l *Log, body string, heads []ID) ID {
	t.Helper()

	c, err := Sign(l.at, []byte(body), heads)
	if err != nil {
		t.Fatal(err)
	}
	id, err := l.Add(c)
	if err != nil {
		t.Fatalf("Add(%q): %v", body, err)
	}
	return id
}

// addedMaybe is a change the log is allowed to refuse: a hostile peer sends what it likes, and
// being turned away is the right answer. What is not allowed is taking it and then being unable to
// read the history.
func addedMaybe(l *Log, body string, heads []ID) ID {
	c, err := sign(l.at, []byte(body), heads, nil)
	if err != nil {
		return ID{}
	}
	id, err := l.Add(c)
	if err != nil {
		return ID{}
	}
	return id
}

func folded(t *testing.T, l *Log, body string, heads, over []ID) ID {
	t.Helper()

	c, err := sign(l.at, []byte(body), heads, over)
	if err != nil {
		t.Fatalf("signing a fold: %v", err)
	}
	id, err := l.Add(c)
	if err != nil {
		// A refused fold is a fine answer; it is a taken one that must not wedge anything.
		return ID{}
	}
	_ = fmt.Sprint(id)
	return id
}

// A history has a size, and calling something a fold must not be the way past it.
//
// A fold is exempt from the limits because it is what makes a full log smaller — a log that could
// not take one could never shrink. But that only holds for a fold that stands for everything there
// is. One standing for a single change writes more than it takes away, and if the word alone bought
// the exemption a peer could send them one after another until the disk was gone.
func TestCallingSomethingAFoldIsNotAWayPastTheLimits(t *testing.T) {
	asSomebody(t)
	l := aLog(t, thing)

	first := added(t, l, "one", nil)

	// A fold that stands for that one change and nothing else, carrying as much as a change may.
	big := make([]byte, MaxBody)
	for i := range big {
		big[i] = byte(i)
	}

	grew := 0
	for range 40 {
		c, err := sign(l.at, big, nil, []ID{first})
		if err != nil {
			break
		}
		if _, err := l.Add(c); err != nil {
			break
		}
		grew++
		if l.size > MaxLog {
			t.Fatalf("this history is %d bytes, over the %d limit, after %d so-called folds", l.size, MaxLog, grew)
		}
	}

	// And a real fold — one that stands for everything here — is still taken, however full it is.
	if _, err := l.Fold([]byte("what it all came to")); err != nil {
		t.Fatalf("a history would not take a fold that stands for all of it: %v", err)
	}
	if n := len(l.changes); n != 1 {
		t.Fatalf("after folding everything the history holds %d changes, want 1", n)
	}
}
