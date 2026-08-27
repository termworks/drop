package proto

import (
	"testing"

	"github.com/bresilla/drop/src/pkg/node"
)

// Getting in somewhere must not buy more guesses somewhere else.
//
// The count is what makes a password worth having: six tries in a window, each costing the guesser
// a frame and this machine 64 MiB of argon2. A peer that may open any ordinary path — a public one,
// or anything it is paired for — could open it, have the count cleared, and go back to guessing,
// for ever, on a path it was never admitted to.
func TestOpeningSomethingElseDoesNotBuyMoreGuesses(t *testing.T) {
	g := &guesses{spent: map[node.ID]guess{}}
	who := idFrom(1)

	for i := range mostGuesses {
		if !g.spare(who) {
			t.Fatalf("guess %d of %d was refused", i+1, mostGuesses)
		}
	}
	if g.spare(who) {
		t.Fatal("a peer got more than its share of guesses")
	}

	// Whatever else it does, the count stands until the window rolls. Only a path that asked for a
	// password forgives one, and this is not that.
	if g.spare(who) {
		t.Fatal("the count was cleared by something that was not a password")
	}

	// And forgetting, when it is earned, does give the tries back.
	g.forget(who)
	if !g.spare(who) {
		t.Fatal("a peer that got a password right was still locked out")
	}
}
