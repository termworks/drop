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
		Mount{Path: "/friends/chat", Archetype: "chat"},
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
		Mount{Path: "/friends/scratch", Archetype: "share", Access: Access{Password: hash}},
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
		Mount{Path: "/private", Archetype: "share"},
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
		Mount{Path: "/b", Archetype: "chat", Access: Access{Named: []string{"carol"}}},
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
		Mount{Path: "/friendsonly", Archetype: "chat"},
	)

	if ok, _ := table.Admits("/friendsonly", bob()); ok {
		t.Fatal("a rule leaked across a name that merely shares a prefix")
	}
}

func TestTheDeepestRuleWins(t *testing.T) {
	table := tree(t,
		Mount{Path: "/one", Access: Access{AnyPaired: true}},
		Mount{Path: "/one/two", Access: Access{Named: []string{"bob"}}},
		Mount{Path: "/one/two/five/eight", Archetype: "share"},
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

// A name on its own is a person: any machine they have signed a badge for.
func TestANameAdmitsAnyMachineOfThatPerson(t *testing.T) {
	rule := Access{Named: []string{"bob"}}

	for _, machine := range []string{"laptop", "phone", "a machine nobody has seen before"} {
		who := Caller{ID: "abc", Name: machine, UserName: "bob", Paired: true}
		if ok, why := rule.Admits(who); !ok {
			t.Errorf("bob's %s was refused: %s", machine, why)
		}
	}

	// Somebody else's machine is not bob's, whatever it is called.
	if ok, _ := rule.Admits(Caller{ID: "abc", Name: "laptop", UserName: "carol", Paired: true}); ok {
		t.Error("carol was admitted by a rule naming bob")
	}
}

// A name with a machine after it is that machine and no other.
func TestANameWithAMachineAdmitsOnlyThatOne(t *testing.T) {
	rule := Access{Named: []string{"bob@laptop"}}

	if ok, why := rule.Admits(Caller{ID: "abc", Name: "laptop", UserName: "bob", Paired: true}); !ok {
		t.Errorf("bob's laptop was refused: %s", why)
	}
	if ok, _ := rule.Admits(Caller{ID: "abc", Name: "phone", UserName: "bob", Paired: true}); ok {
		t.Error("bob's phone was admitted by a rule naming his laptop")
	}
}

// A device paired with --machine belongs to nobody here and answers to its own name. It is the only
// way to write a rule for a build server, or anything else that is not somebody's identity.
func TestAMachinePairedOnItsOwnIsNamedByItself(t *testing.T) {
	rule := Access{Named: []string{"buildbox"}}

	if ok, why := rule.Admits(Caller{ID: "abc", Name: "buildbox", Paired: true}); !ok {
		t.Errorf("a machine paired on its own was refused by its own name: %s", why)
	}
}

// A public path is reachable by whoever knows the id, paired or not. It is the only rule that
// admits a stranger, and it has to be asked for.
func TestAPublicPathAdmitsAnybody(t *testing.T) {
	stranger := Caller{ID: "somebody nobody has met"}

	if ok, _ := (Access{}).Admits(stranger); ok {
		t.Fatal("a path with no rule admitted a stranger")
	}
	if ok, _ := (Access{AnyPaired: true}).Admits(stranger); ok {
		t.Fatal("a paired-only path admitted a stranger")
	}
	if ok, why := (Access{Anyone: true}).Admits(stranger); !ok {
		t.Fatalf("a public path refused a stranger: %s", why)
	}
}

// A visible path says it exists and refuses to be opened. It is the rung between shared and
// secret: somebody can ask for it by name rather than having to be told it is there.
func TestAVisiblePathIsSeenButNotOpened(t *testing.T) {
	rule := Access{Named: []string{"bob"}, Visible: []string{"carol"}}

	carol := Caller{ID: "abc", Name: "laptop", UserName: "carol", Paired: true}
	if ok, _ := rule.Admits(carol); ok {
		t.Error("a visible path let somebody in")
	}
	if !rule.Sees(carol) {
		t.Error("a visible path was hidden from the person it is visible to")
	}

	// Somebody it says nothing about learns nothing.
	dave := Caller{ID: "def", Name: "phone", UserName: "dave", Paired: true}
	if rule.Sees(dave) {
		t.Error("a visible path was shown to somebody it does not name")
	}

	// And whoever may open it can obviously see it.
	bob := Caller{ID: "ghi", Name: "laptop", UserName: "bob", Paired: true}
	if !rule.Sees(bob) {
		t.Error("somebody who may open a path cannot see it")
	}
}

// Visible to anybody paired, which is the ordinary way to put something up to be asked for.
func TestAPathCanBeVisibleToEveryonePaired(t *testing.T) {
	rule := Access{Named: []string{"bob"}, AnyVisible: true}

	stranger := Caller{ID: "abc"}
	if rule.Sees(stranger) {
		t.Error("a stranger saw a path that is visible to paired devices")
	}

	carol := Caller{ID: "def", Name: "laptop", UserName: "carol", Paired: true}
	if !rule.Sees(carol) {
		t.Error("somebody paired could not see it")
	}
	if ok, _ := rule.Admits(carol); ok {
		t.Error("being able to see it let them in")
	}
}

// A refusal beats being visible too, or revoking somebody would still leave them able to see what
// they used to reach and ask for it again.
func TestARefusalHidesAPathAsWell(t *testing.T) {
	rule := Access{AnyVisible: true, Refused: []string{"bob"}}

	bob := Caller{ID: "abc", Name: "laptop", UserName: "bob", Paired: true}
	if rule.Sees(bob) {
		t.Error("somebody refused could still see the path")
	}
}

// Pairing is recognition; trust is the second, deliberate step. A narrow rule is written against
// the second, or "everybody I have ever met" and "everybody I trust" would be the same set.
func TestTrustedIsNarrowerThanPaired(t *testing.T) {
	met := Caller{ID: "abc", Name: "laptop", Paired: true}
	trusted := Caller{ID: "def", Name: "desk", Paired: true, Trusted: true}

	wide := Access{AnyPaired: true}
	if ok, why := wide.Admits(met); !ok {
		t.Errorf("a paired device was refused a paired rule: %s", why)
	}

	narrow := Access{AnyTrusted: true}
	if ok, _ := narrow.Admits(met); ok {
		t.Error("somebody merely paired with got into a trusted rule")
	}
	if ok, why := narrow.Admits(trusted); !ok {
		t.Errorf("a trusted device was refused: %s", why)
	}

	// And an unpaired stranger is neither.
	if ok, _ := narrow.Admits(Caller{ID: "ghi", Trusted: true}); ok {
		t.Error("an unpaired caller claiming trust got in")
	}
}

// Visibility follows the same line: something put up to be asked for is shown to the people you
// trust, not to everybody you have ever met.
func TestVisibilityCanFollowTrust(t *testing.T) {
	rule := Access{TrustedVisible: true}

	met := Caller{ID: "abc", Name: "laptop", Paired: true}
	if rule.Sees(met) {
		t.Error("somebody merely paired with saw a path visible to trusted devices")
	}

	trusted := Caller{ID: "def", Name: "desk", Paired: true, Trusted: true}
	if !rule.Sees(trusted) {
		t.Error("a trusted device could not see it")
	}
	if ok, _ := rule.Admits(trusted); ok {
		t.Error("being trusted enough to see it was enough to open it")
	}
}

// The table is read from a goroutine per connection while a cast goes up and a handoff goes down on
// another. Reading the mounts without the lock is a fatal runtime error that no recover catches, so
// what is at stake is the whole daemon and every connection it holds.
func TestTheTableIsJudgedWhileMountsComeAndGo(t *testing.T) {
	table := NewTable()
	if err := table.Add(Mount{Path: "/work", Archetype: "files", Access: Access{AnyPaired: true}}); err != nil {
		t.Fatalf("adding /work: %v", err)
	}

	churning := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-churning:
				return
			default:
			}
			_ = table.Add(Mount{Path: "/cast", Archetype: "tty", Access: Access{AnyTrusted: true}})
			table.Drop("/cast")
		}
	}()

	for range 20000 {
		if _, found := table.AccessFor("/work/deep/enough"); !found {
			t.Fatal("the rule on /work stopped governing what is under it")
		}
	}
	close(churning)
	<-stopped
}

// A guess costs this machine 64 MiB and three passes of argon2, and it is whoever dialled that
// chooses when to spend them. A caller some other rule already admits must never reach the hash.
func TestACallerAdmittedOtherwiseNeverPaysForAGuess(t *testing.T) {
	hash, err := passwd.Hash("let me in")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}

	guarded := Access{AnyPaired: true, Password: hash}
	paired := Caller{ID: "aaaa", Name: "bob", Paired: true, Password: "nope"}

	// Counted rather than timed: how many guesses were paid for is the question, and a stopwatch
	// answers it only on a machine doing nothing else.
	before := passwd.Spent()

	if ok, _ := guarded.Admits(paired); !ok {
		t.Fatal("a paired caller was refused a path shared with anyone paired")
	}
	if paid := passwd.Spent() - before; paid != 0 {
		t.Errorf("admitting a paired caller paid for %d guesses", paid)
	}
}

// Under All every rule has to pass, so one that has already failed settles it. Hashing afterwards
// is work a stranger asked for and nothing turns on.
func TestARuleThatAlreadyFailedDoesNotReachTheHash(t *testing.T) {
	hash, err := passwd.Hash("let me in")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}

	both := Access{All: true, AnyPaired: true, Password: hash}
	before := passwd.Spent()

	if ok, _ := both.Admits(Caller{ID: "cccc", Password: "let me in"}); ok {
		t.Fatal("a path needing pairing and a password was opened with the password alone")
	}
	if paid := passwd.Spent() - before; paid != 0 {
		t.Errorf("refusing an unpaired caller paid for %d guesses", paid)
	}
}
