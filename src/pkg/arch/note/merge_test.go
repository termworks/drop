package note

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/history"
)

// What happens when two people change one file at the same time.
//
// Nothing here signs anything or touches a disk: a change is its heads, its author and what they
// saved, and the merge is a function of those. What is being checked is the one property somebody
// asked for — that two people editing one file both keep what they wrote — and the one that makes
// it work at all, that every machine arrives at the same file.

// saved is one change, as somebody who had seen those changes.
func saved(author, body string, heads ...history.ID) history.Change {
	return history.Change{Heads: sorted(append([]history.ID(nil), heads...)), Author: author, Body: []byte(body)}
}

// asPeople is what these tests call the people making the changes.
func asPeople(author string) string { return strings.TrimSuffix(author, "-key") }

func whole(t *testing.T, changes ...history.Change) ([]byte, []Aside) {
	t.Helper()
	return Whole(changes, asPeople)
}

func TestTwoPeopleEditDifferentPartsAndBothSurvive(t *testing.T) {
	first := saved("alice-key", "one\ntwo\nthree\nfour\nfive\n")
	alice := saved("alice-key", "ONE\ntwo\nthree\nfour\nfive\n", first.ID())
	bob := saved("bob-key", "one\ntwo\nthree\nfour\nFIVE\n", first.ID())

	body, aside := whole(t, first, alice, bob)
	if len(aside) != 0 {
		t.Fatalf("something was put aside: %+v", aside)
	}
	if got, want := string(body), "ONE\ntwo\nthree\nfour\nFIVE\n"; got != want {
		t.Fatalf("the merge is\n%s\nwant\n%s", got, want)
	}
}

func TestTwoPeopleEditTheSameLineAndBothArePreserved(t *testing.T) {
	first := saved("alice-key", "one\ntwo\nthree\n")
	alice := saved("alice-key", "one\nby alice\nthree\n", first.ID())
	bob := saved("bob-key", "one\nby bob\nthree\n", first.ID())

	body, _ := whole(t, first, alice, bob)
	text := string(body)
	t.Logf("the conflicted file:\n%s", text)

	for _, want := range []string{"<<<<<<< ", "=======\n", ">>>>>>> ", "alice", "bob", "by alice\n", "by bob\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the merge does not hold %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(text, "one\n") || !strings.HasSuffix(text, "three\n") {
		t.Fatalf("what nobody touched did not survive:\n%s", text)
	}
}

func TestSomebodyWhoSawTheOtherChangeInventsNoConflict(t *testing.T) {
	first := saved("alice-key", "one\ntwo\nthree\n")
	alice := saved("alice-key", "one\nby alice\nthree\n", first.ID())
	bob := saved("bob-key", "one\nby bob\nthree\n", alice.ID())

	body, aside := whole(t, first, alice, bob)
	if len(aside) != 0 {
		t.Fatalf("something was put aside: %+v", aside)
	}
	if got, want := string(body), "one\nby bob\nthree\n"; got != want {
		t.Fatalf("the merge is\n%s\nwant\n%s", got, want)
	}
}

func TestTheSameChangesInAnyOrderMakeTheSameFile(t *testing.T) {
	first := saved("alice-key", "one\ntwo\nthree\nfour\n")
	alice := saved("alice-key", "ONE\ntwo\nthree\nfour\n", first.ID())
	bob := saved("bob-key", "one\ntwo\nTHREE\nfour\n", first.ID())
	// Carol had seen alice's change, so what she saved is alice's file with her own edit in it.
	carol := saved("carol-key", "ONE\ntwo\nthree\nFOUR\n", alice.ID())

	orders := [][]history.Change{
		{first, alice, bob, carol},
		{first, bob, alice, carol},
		{carol, bob, alice, first},
		{first, carol, alice, bob},
	}

	var was []byte
	for i, order := range orders {
		body, _ := whole(t, order...)
		if i == 0 {
			was = body
			continue
		}
		if !bytes.Equal(body, was) {
			t.Fatalf("order %d makes\n%s\nand order 0 makes\n%s", i, body, was)
		}
	}
	if !strings.Contains(string(was), "ONE") || !strings.Contains(string(was), "THREE") || !strings.Contains(string(was), "FOUR") {
		t.Fatalf("somebody's edit is missing:\n%s", was)
	}
}

func TestThreePeopleOneRoundEachEndUpWithTheSameFile(t *testing.T) {
	first := saved("alice-key", "one\ntwo\nthree\nfour\nfive\nsix\n")
	alice := saved("alice-key", "ALICE\ntwo\nthree\nfour\nfive\nsix\n", first.ID())
	bob := saved("bob-key", "one\ntwo\nBOB\nfour\nfive\nsix\n", first.ID())
	carol := saved("carol-key", "one\ntwo\nthree\nfour\nfive\nCAROL\n", first.ID())

	// Each of the three replays what it holds in the order it happened to receive it.
	mine, _ := whole(t, first, alice, bob, carol)
	yours, _ := whole(t, bob, first, carol, alice)
	theirs, _ := whole(t, carol, alice, first, bob)

	if !bytes.Equal(mine, yours) || !bytes.Equal(mine, theirs) {
		t.Fatalf("three machines hold three files:\n%s\n---\n%s\n---\n%s", mine, yours, theirs)
	}
	if got, want := string(mine), "ALICE\ntwo\nBOB\nfour\nfive\nCAROL\n"; got != want {
		t.Fatalf("the merge is\n%s\nwant\n%s", got, want)
	}
}

func TestSomethingThatIsNotTextIsKeptBothWaysAndNeverMerged(t *testing.T) {
	// The header of a SQLite file, which is what merging line by line would destroy.
	base := "SQLite format 3\x00\x10\x00\x01\x01\x00@  \x00\x00\x00\x01"
	first := saved("alice-key", base)
	alice := saved("alice-key", base+"\x01alice\x00", first.ID())
	bob := saved("bob-key", base+"\x02bob\x00", first.ID())

	body, aside := whole(t, first, alice, bob)
	if len(aside) != 1 {
		t.Fatalf("the other version was not kept: %+v", aside)
	}
	if strings.Contains(string(body), "<<<<<<<") {
		t.Fatalf("a database was merged as lines:\n%q", body)
	}

	kept := [][]byte{body, aside[0].Body}
	for _, want := range []string{"alice", "bob"} {
		found := false
		for _, held := range kept {
			if bytes.Contains(held, []byte(want)) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s's version is gone: file %q, aside %q", want, body, aside[0].Body)
		}
	}
	if aside[0].Who != "alice" && aside[0].Who != "bob" {
		t.Fatalf("what was kept aside is filed under %q", aside[0].Who)
	}
}

func TestWhatCountsAsTextIsAskedConservatively(t *testing.T) {
	yes := []string{"", "one\ntwo\n", "no ending", "a\tb\r\n", "héllo\n"}
	for _, raw := range yes {
		if !textual([]byte(raw)) {
			t.Errorf("%q is text and was refused", raw)
		}
	}

	no := []string{"\x00", "SQLite format 3\x00", "\xff\xfe\x00\x00", strings.Repeat("x", maxLine+1)}
	for _, raw := range no {
		if textual([]byte(raw)) {
			t.Errorf("%q is not text and was taken for it", raw)
		}
	}
}

func TestALineAddedAtEachEndIsNotAConflict(t *testing.T) {
	first := saved("alice-key", "middle\n")
	alice := saved("alice-key", "top\nmiddle\n", first.ID())
	bob := saved("bob-key", "middle\nbottom\n", first.ID())

	body, _ := whole(t, first, alice, bob)
	if got, want := string(body), "top\nmiddle\nbottom\n"; got != want {
		t.Fatalf("the merge is\n%s\nwant\n%s", got, want)
	}
}

func TestAFileWithNoHistoryIsNoFile(t *testing.T) {
	if body, aside := whole(t); body != nil || aside != nil {
		t.Fatalf("nothing made %q and %+v", body, aside)
	}
}
