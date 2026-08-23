package ns

import (
	"testing"

	"github.com/bresilla/drop/src/pkg/passwd"
)

func bob() Caller   { return Caller{ID: "aaaa", Name: "bob", Paired: true} }
func carol() Caller { return Caller{ID: "bbbb", Name: "carol", Paired: true} }

// A stranger: the handshake proved a key, but nothing here knows it.
func stranger() Caller { return Caller{ID: "cccc"} }

// The rule the whole design rests on. Forgetting to write access must close a path.
func TestNothingDeclaredAdmitsNobody(t *testing.T) {
	var nothing Access

	for _, who := range []Caller{bob(), carol(), stranger()} {
		if ok, _ := nothing.Admits(who); ok {
			t.Fatalf("%s got in through a path with no rule", who.Name)
		}
	}
}

// A connection with no proven identity is not a caller at all.
func TestAnUnidentifiedCallerIsRefused(t *testing.T) {
	open := Access{AnyPaired: true}

	if ok, why := open.Admits(Caller{Paired: true, Name: "bob"}); ok {
		t.Fatalf("a caller with no id was admitted: %s", why)
	}
}

func TestNamedDevicesGetIn(t *testing.T) {
	rule := Access{Named: []string{"bob"}}

	if ok, why := rule.Admits(bob()); !ok {
		t.Fatalf("bob was refused: %s", why)
	}
	if ok, _ := rule.Admits(carol()); ok {
		t.Fatal("carol was admitted by a rule naming only bob")
	}
}

// Being in the book is not the same as being paired with. A name without a shared secret behind it
// must not open anything.
func TestANamedButUnpairedDeviceIsRefused(t *testing.T) {
	rule := Access{Named: []string{"bob"}}
	pinned := Caller{ID: "aaaa", Name: "bob", Paired: false}

	if ok, _ := rule.Admits(pinned); ok {
		t.Fatal("a pinned but unpaired device was admitted")
	}
}

func TestAnyPairedGetsIn(t *testing.T) {
	rule := Access{AnyPaired: true}

	if ok, why := rule.Admits(carol()); !ok {
		t.Fatalf("a paired device was refused: %s", why)
	}
	if ok, _ := rule.Admits(stranger()); ok {
		t.Fatal("an unpaired device was admitted by a paired-only rule")
	}
}

// A bare key is a real cryptographic statement: the handshake proved possession before this runs.
func TestABareKeyGetsInWithoutPairing(t *testing.T) {
	rule := Access{Keys: []string{"cccc"}}

	if ok, why := rule.Admits(stranger()); !ok {
		t.Fatalf("a named key was refused: %s", why)
	}
	if ok, _ := rule.Admits(bob()); ok {
		t.Fatal("a different key was admitted")
	}
}

func TestKeysAreComparedWithoutCase(t *testing.T) {
	rule := Access{Keys: []string{"AABBCC"}}

	if ok, _ := rule.Admits(Caller{ID: "aabbcc"}); !ok {
		t.Fatal("the same key in another case was refused")
	}
}

func TestAPasswordGetsIn(t *testing.T) {
	hash, err := passwd.Hash("open sesame")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	rule := Access{Password: hash}

	if ok, why := rule.Admits(Caller{ID: "cccc", Password: "open sesame"}); !ok {
		t.Fatalf("the right password was refused: %s", why)
	}
	if ok, _ := rule.Admits(Caller{ID: "cccc", Password: "wrong"}); ok {
		t.Fatal("a wrong password was admitted")
	}
	if ok, _ := rule.Admits(Caller{ID: "cccc"}); ok {
		t.Fatal("no password at all was admitted")
	}
}

// Any one rule is enough by default, which is what makes "bob, or whoever has the word" expressible.
func TestAnyRuleIsEnoughByDefault(t *testing.T) {
	hash, _ := passwd.Hash("word")
	rule := Access{Named: []string{"bob"}, Password: hash}

	if ok, _ := rule.Admits(bob()); !ok {
		t.Fatal("bob was refused although he is named")
	}
	if ok, _ := rule.Admits(Caller{ID: "zzzz", Password: "word"}); !ok {
		t.Fatal("the password holder was refused")
	}
}

// require = "all" is the second factor: being bob is not enough on its own.
func TestRequireAllNeedsEveryRule(t *testing.T) {
	hash, _ := passwd.Hash("word")
	rule := Access{Named: []string{"bob"}, Password: hash, All: true}

	if ok, _ := rule.Admits(bob()); ok {
		t.Fatal("bob got in without the password")
	}
	if ok, _ := rule.Admits(Caller{ID: "zzzz", Password: "word"}); ok {
		t.Fatal("the password alone got in")
	}

	both := bob()
	both.Password = "word"
	if ok, why := rule.Admits(both); !ok {
		t.Fatalf("bob with the password was refused: %s", why)
	}
}

// ------------------------------------------------------------------ inheritance

func tree(t *testing.T, mounts ...Mount) *Table {
	t.Helper()

	table := NewTable()
	for _, m := range mounts {
		if err := table.Add(m); err != nil {
			t.Fatalf("adding %s: %v", m.Path, err)
		}
	}
	return table
}

func TestAccessInheritsDownThePath(t *testing.T) {
	table := tree(t,
		Mount{Path: "/friends", Access: Access{Named: []string{"bob", "carol"}}},
		Mount{Path: "/friends/chat", Kind: KindChat},
	)

	if ok, why := table.Admits("/friends/chat", bob()); !ok {
		t.Fatalf("bob was refused a path under his own branch: %s", why)
	}
	if ok, _ := table.Admits("/friends/chat", stranger()); ok {
		t.Fatal("a stranger reached a path under a named branch")
	}
}

// The answer you gave: a declaration replaces what it inherited rather than adding to it.
func TestADeeperRuleReplacesTheOneAbove(t *testing.T) {
	hash, _ := passwd.Hash("word")
	table := tree(t,
		Mount{Path: "/friends", Access: Access{Named: []string{"bob", "carol"}}},
		Mount{Path: "/friends/scratch", Kind: KindFiles, Access: Access{Password: hash}},
	)

	// carol still has the branch
	if ok, _ := table.Admits("/friends", carol()); !ok {
		t.Fatal("carol lost the branch")
	}
	// but not the path that redeclared
	if ok, _ := table.Admits("/friends/scratch", carol()); ok {
		t.Fatal("the deeper rule merged with the one above instead of replacing it")
	}
	if ok, why := table.Admits("/friends/scratch", Caller{ID: "zzzz", Password: "word"}); !ok {
		t.Fatalf("the deeper rule did not apply: %s", why)
	}
}

func TestAPathWithNoRuleAnywhereAboveIsClosed(t *testing.T) {
	table := tree(t,
		Mount{Path: "/friends", Access: Access{Named: []string{"bob"}}},
		Mount{Path: "/private", Kind: KindFiles},
	)

	if ok, _ := table.Admits("/private", bob()); ok {
		t.Fatal("a path outside every declared branch was open")
	}
	if _, found := table.AccessFor("/private"); found {
		t.Fatal("a rule was found where none was declared")
	}
}

// A sibling's rule must not leak sideways.
func TestARuleDoesNotReachASibling(t *testing.T) {
	table := tree(t,
		Mount{Path: "/a", Access: Access{Named: []string{"bob"}}},
		Mount{Path: "/b", Kind: KindChat, Access: Access{Named: []string{"carol"}}},
	)

	if ok, _ := table.Admits("/b", bob()); ok {
		t.Fatal("bob reached carol's path")
	}
	if ok, _ := table.Admits("/a", carol()); ok {
		t.Fatal("carol reached bob's path")
	}
}

// Prefix matching has to respect segment boundaries, or /friendsX inherits from /friends.
func TestARuleDoesNotReachAPathThatMerelyStartsTheSame(t *testing.T) {
	table := tree(t,
		Mount{Path: "/friends", Access: Access{Named: []string{"bob"}}},
		Mount{Path: "/friendsonly", Kind: KindChat},
	)

	if ok, _ := table.Admits("/friendsonly", bob()); ok {
		t.Fatal("a rule leaked across a name that merely shares a prefix")
	}
}

func TestTheDeepestRuleWins(t *testing.T) {
	table := tree(t,
		Mount{Path: "/one", Access: Access{AnyPaired: true}},
		Mount{Path: "/one/two", Access: Access{Named: []string{"bob"}}},
		Mount{Path: "/one/two/five/eight", Kind: KindFiles},
	)

	if ok, _ := table.Admits("/one/two/five/eight", carol()); ok {
		t.Fatal("carol reached a path governed by a bob-only rule two levels up")
	}
	if ok, why := table.Admits("/one/two/five/eight", bob()); !ok {
		t.Fatalf("bob was refused a path under his rule: %s", why)
	}
	if ok, _ := table.Admits("/one", carol()); !ok {
		t.Fatal("carol lost the shallower branch")
	}
}
