package proto

import (
	"testing"

	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/passwd"
)

func paths(shown []Served) map[string]bool {
	out := map[string]bool{}
	for _, s := range shown {
		out[s.Path] = true
	}
	return out
}

// The property the design rests on: a listing is filtered, not annotated. Bob must not learn that
// a terminal exists, because knowing which machine has one is most of the work of attacking it.
func TestALedgerShowsOnlyWhatTheCallerMayReach(t *testing.T) {
	table := served(t,
		ns.Mount{Path: "/friends", Access: ns.Access{Named: []string{"bob"}}},
		ns.Mount{Path: "/friends/chat", Archetype: ns.Chat},
		ns.Mount{Path: "/work", Access: ns.Access{Named: []string{"laptop"}}},
		ns.Mount{Path: "/work/term", Archetype: ns.TTY},
	)

	bob := ns.Caller{ID: "aaaa", Name: "bob", Paired: true}
	shown := paths(Describe(table, bob))

	if !shown["/friends/chat"] {
		t.Fatal("bob was not shown his own path")
	}
	for _, hidden := range []string{"/work", "/work/term"} {
		if shown[hidden] {
			t.Fatalf("bob was shown %s", hidden)
		}
	}
}

func TestAStrangerIsShownNothing(t *testing.T) {
	table := served(t,
		ns.Mount{Path: "/friends", Access: ns.Access{AnyPaired: true}},
		ns.Mount{Path: "/friends/chat", Archetype: ns.Chat},
	)

	if shown := Describe(table, ns.Caller{ID: "zzzz"}); len(shown) != 0 {
		t.Fatalf("an unpaired caller was shown %+v", shown)
	}
}

// A path with no rule is invisible to everyone, including someone otherwise well trusted.
func TestAPathWithNoRuleIsInNobodysListing(t *testing.T) {
	table := served(t,
		ns.Mount{Path: "/friends", Access: ns.Access{Named: []string{"bob"}}},
		ns.Mount{Path: "/friends/chat", Archetype: ns.Chat},
		ns.Mount{Path: "/secret", Archetype: ns.Share},
	)

	bob := ns.Caller{ID: "aaaa", Name: "bob", Paired: true}
	if paths(Describe(table, bob))["/secret"] {
		t.Fatal("a path with no rule was listed")
	}
}

// Nobody offers a password to ask what exists, so a password path is in no listing at all. Whoever
// is given one needs the path as well as the word.
func TestAPasswordPathIsNotListed(t *testing.T) {
	hash, err := passwd.Hash("word")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}

	table := served(t,
		ns.Mount{Path: "/handoff", Archetype: ns.Share, Access: ns.Access{Password: hash}},
	)

	for _, who := range []ns.Caller{
		{ID: "aaaa", Name: "bob", Paired: true},
		{ID: "zzzz"},
	} {
		if len(Describe(table, who)) != 0 {
			t.Fatalf("%s was shown a password path", who.ID)
		}
	}

	// It opens for someone who has the word, though — it is hidden, not closed.
	withWord := ns.Caller{ID: "zzzz", Password: "word"}
	if ok, why := table.Admits("/handoff", withWord); !ok {
		t.Fatalf("the word did not open it: %s", why)
	}
}

// A bare key is enough to be shown something, without any pairing.
func TestAKeyIsShownItsOwnPath(t *testing.T) {
	table := served(t,
		ns.Mount{Path: "/ci", Archetype: ns.Share, Access: ns.Access{Keys: []string{"cccc"}}},
		ns.Mount{Path: "/friends", Archetype: ns.Chat, Access: ns.Access{AnyPaired: true}},
	)

	shown := paths(Describe(table, ns.Caller{ID: "cccc"}))
	if !shown["/ci"] {
		t.Fatal("the key was not shown its own path")
	}
	if shown["/friends"] {
		t.Fatal("the key was shown a paired-only path")
	}
}

func TestDescribeHandlesNoTable(t *testing.T) {
	if got := Describe(nil, ns.Caller{ID: "aaaa"}); got != nil {
		t.Fatalf("got %+v", got)
	}
}
