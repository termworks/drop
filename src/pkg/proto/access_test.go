package proto

import (
	"strings"
	"testing"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/passwd"
)

func who(seed byte) node.ID {
	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

func served(t *testing.T, mounts ...ns.Mount) *ns.Table {
	t.Helper()

	table := ns.NewTable()
	for _, m := range mounts {
		if err := table.Add(m); err != nil {
			t.Fatalf("adding %s: %v", m.Path, err)
		}
	}
	return table
}

// The chain end to end: a branch carries the rule, a leaf inherits it, and resolve enforces it.
func TestResolveHonoursTheInheritedRule(t *testing.T) {
	bob, carol := who(1), who(2)

	table := served(t,
		ns.Mount{Path: "/friends", Access: ns.Access{Named: []string{"bob"}}},
		ns.Mount{Path: "/friends/chat", Archetype: ns.Chat},
	)

	asBob := ns.Caller{ID: bob.String(), Name: "bob", Paired: true}
	if _, err := resolve(table, bob, asBob, Open{Mode: ModeMessages, Path: "/friends/chat"}); err != nil {
		t.Fatalf("bob was refused: %v", err)
	}

	asCarol := ns.Caller{ID: carol.String(), Name: "carol", Paired: true}
	if _, err := resolve(table, carol, asCarol, Open{Mode: ModeMessages, Path: "/friends/chat"}); err == nil {
		t.Fatal("carol reached a path shared only with bob")
	}
}

// The failure that matters most: a path nobody was granted must not be reachable, even by a device
// that is paired and otherwise trusted.
func TestAPathWithNoRuleIsUnreachable(t *testing.T) {
	bob := who(1)
	table := served(t, ns.Mount{Path: "/term", Archetype: ns.TTY})

	asBob := ns.Caller{ID: bob.String(), Name: "bob", Paired: true}
	_, err := resolve(table, bob, asBob, Open{Mode: ModeDuplex, Path: "/term"})
	if err == nil {
		t.Fatal("a path with no access rule was served to a paired device")
	}
	if !strings.Contains(err.Error(), "shared with") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// A password travels on the Open frame and has to reach the rule that checks it.
func TestAPasswordOnTheWireOpensAPath(t *testing.T) {
	hash, err := passwd.Hash("let me in")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}

	stranger := who(9)
	table := served(t, ns.Mount{Path: "/handoff", Archetype: ns.Share, Access: ns.Access{Password: hash}})

	// Unpaired, unnamed, unknown — and still admitted, because it knew the word.
	asStranger := ns.Caller{ID: stranger.String(), Password: "let me in"}
	if _, err := resolve(table, stranger, asStranger, Open{Mode: ModeFiles, Path: "/handoff"}); err != nil {
		t.Fatalf("the password was refused: %v", err)
	}

	wrong := ns.Caller{ID: stranger.String(), Password: "nope"}
	if _, err := resolve(table, stranger, wrong, Open{Mode: ModeFiles, Path: "/handoff"}); err == nil {
		t.Fatal("a wrong password opened the path")
	}
}

// A bare key admits a machine that never paired.
func TestABareKeyOpensAPath(t *testing.T) {
	ci := who(7)
	table := served(t, ns.Mount{
		Path:      "/ci",
		Archetype: ns.Share,
		Access:    ns.Access{Keys: []string{ci.String()}},
	})

	asCI := ns.Caller{ID: ci.String()}
	if _, err := resolve(table, ci, asCI, Open{Mode: ModeFiles, Path: "/ci"}); err != nil {
		t.Fatalf("the named key was refused: %v", err)
	}

	other := who(8)
	if _, err := resolve(table, other, ns.Caller{ID: other.String()}, Open{Mode: ModeFiles, Path: "/ci"}); err == nil {
		t.Fatal("a different key was admitted")
	}
}

// A branch carries a rule but serves nothing, so opening one is a mistake with a clear answer.
func TestABranchCannotBeOpened(t *testing.T) {
	bob := who(1)
	table := served(t,
		ns.Mount{Path: "/friends", Access: ns.Access{Named: []string{"bob"}}},
		ns.Mount{Path: "/friends/chat", Archetype: ns.Chat},
	)

	asBob := ns.Caller{ID: bob.String(), Name: "bob", Paired: true}
	_, err := resolve(table, bob, asBob, Open{Mode: ModeMessages, Path: "/friends"})
	if err == nil {
		t.Fatal("a branch was opened as though it served something")
	}
	if !strings.Contains(err.Error(), "serves nothing") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// The secret must survive the frame it travels in.
func TestTheSecretSurvivesEncoding(t *testing.T) {
	open := Open{Mode: ModeFiles, From: "bob", Path: "/handoff", Secret: "let me in"}

	got, err := decodeOpen(open.encode())
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Secret != "let me in" {
		t.Fatalf("secret = %q", got.Secret)
	}
	if got.Path != "/handoff" || got.From != "bob" || got.Mode != ModeFiles {
		t.Fatalf("the rest of the frame did not survive: %+v", got)
	}
}
