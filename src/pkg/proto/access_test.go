package proto

import (
	"strings"
	"testing"
	"time"

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
		ns.Mount{Path: "/friends/chat", Archetype: "chat"},
	)

	asBob := ns.Caller{ID: bob.String(), Name: "bob", Paired: true}
	if _, _, err := resolve(table, asBob, "/friends/chat"); err != nil {
		t.Fatalf("bob was refused: %v", err)
	}

	asCarol := ns.Caller{ID: carol.String(), Name: "carol", Paired: true}
	if _, _, err := resolve(table, asCarol, "/friends/chat"); err == nil {
		t.Fatal("carol reached a path shared only with bob")
	}
}

// The failure that matters most: a path nobody was granted must not be reachable, even by a device
// that is paired and otherwise trusted.
func TestAPathWithNoRuleIsUnreachable(t *testing.T) {
	bob := who(1)
	table := served(t, ns.Mount{Path: "/term", Archetype: "tty"})

	asBob := ns.Caller{ID: bob.String(), Name: "bob", Paired: true}
	_, _, err := resolve(table, asBob, "/term")
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

	// Paired, so the path is one they may know about and ask for; the password is still wrong.
	stranger := who(9)
	table := served(t, ns.Mount{Path: "/handoff", Archetype: "share", Access: ns.Access{Password: hash}})

	// Unpaired, unnamed, unknown — and still admitted, because it knew the word.
	asStranger := ns.Caller{ID: stranger.String(), Password: "let me in"}
	if _, _, err := resolve(table, asStranger, "/handoff"); err != nil {
		t.Fatalf("the password was refused: %v", err)
	}

	wrong := ns.Caller{ID: stranger.String(), Password: "nope"}
	if _, _, err := resolve(table, wrong, "/handoff"); err == nil {
		t.Fatal("a wrong password opened the path")
	}
}

// A bare key admits a machine that never paired.
func TestABareKeyOpensAPath(t *testing.T) {
	ci := who(7)
	table := served(t, ns.Mount{
		Path:      "/ci",
		Archetype: "share",
		Access:    ns.Access{Keys: []string{ci.String()}},
	})

	asCI := ns.Caller{ID: ci.String()}
	if _, _, err := resolve(table, asCI, "/ci"); err != nil {
		t.Fatalf("the named key was refused: %v", err)
	}

	other := who(8)
	if _, _, err := resolve(table, ns.Caller{ID: other.String()}, "/ci"); err == nil {
		t.Fatal("a different key was admitted")
	}
}

// A branch carries a rule but serves nothing, so opening one is a mistake with a clear answer.
func TestABranchCannotBeOpened(t *testing.T) {
	bob := who(1)
	table := served(t,
		ns.Mount{Path: "/friends", Access: ns.Access{Named: []string{"bob"}}},
		ns.Mount{Path: "/friends/chat", Archetype: "chat"},
	)

	asBob := ns.Caller{ID: bob.String(), Name: "bob", Paired: true}
	_, _, err := resolve(table, asBob, "/friends")
	if err == nil {
		t.Fatal("a branch was opened as though it served something")
	}
	if !strings.Contains(err.Error(), "serves nothing") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// The secret must survive the frame it travels in, and so must what the caller expects to find.
func TestTheOpenSurvivesEncoding(t *testing.T) {
	open := Opening{Archetype: "share", Version: 2, From: "bob", Path: "/handoff", Secret: "let me in"}

	got, err := decodeOpen(open.encode())
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Secret != "let me in" {
		t.Fatalf("secret = %q", got.Secret)
	}
	if got.Path != "/handoff" || got.From != "bob" || got.Archetype != "share" || got.Version != 2 {
		t.Fatalf("the rest of the frame did not survive: %+v", got)
	}
}

// A password path is reachable by anybody who knows this device's id — that is what it is for — so
// every guess costs this machine 64 MiB and three passes of argon2. It must cost that once per
// session, not once per question asked about the same guess.
func TestAGuessIsPaidForOnce(t *testing.T) {
	hash, err := passwd.Hash("let me in")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}

	// Paired, so the path is one they may know about and ask for; the password is still wrong.
	stranger := who(9)
	table := served(t, ns.Mount{
		Path:      "/handoff",
		Archetype: "share",
		Access:    ns.Access{Password: hash, AnyVisible: true},
	})

	// One caller, one memory. The wrong guess is put to the rule and then, on the way to saying
	// the path may be asked for, to the same rule again.
	caller := ns.Caller{ID: stranger.String(), Paired: true, Password: "nope", Tried: passwd.NewTried()}

	first := time.Now()
	if ok, _ := table.Admits("/handoff", caller); ok {
		t.Fatal("a wrong password opened the path")
	}
	one := time.Since(first)

	second := time.Now()
	if !table.Sees("/handoff", caller) {
		t.Fatal("a visible path was not visible")
	}
	two := time.Since(second)

	if two > one {
		t.Errorf("asking the second time cost %v against %v for the first", two, one)
	}
}

// What a peer may offer as a password is bounded before any of it is hashed.
func TestAnEnormousSecretIsRefusedBeforeItIsHashed(t *testing.T) {
	open := Opening{Path: "/handoff", Secret: strings.Repeat("a", MaxSecret+1)}

	if _, err := decodeOpen(open.encode()); err == nil {
		t.Fatalf("a %d byte password was taken off the wire", MaxSecret+1)
	}

	fits := Opening{Path: "/handoff", Secret: strings.Repeat("a", MaxSecret)}
	if _, err := decodeOpen(fits.encode()); err != nil {
		t.Fatalf("a password of the length that is allowed was refused: %v", err)
	}
}

// denying is what the interface leaves behind when somebody is revoked at a path. Like the real
// store, a rule covers everything below where it was written.
type denying struct {
	at  string
	who []string
}

func (d denying) For(path string) (allow, deny []string) {
	if path == d.at || strings.HasPrefix(path, d.at+"/") {
		return nil, d.who
	}
	return nil, nil
}

// Revoking somebody at a path below a mount is the ordinary thing to do -- a grant covers what is
// under it the way a mount does -- and the session has to be judged over the path that was asked
// for, not over the mount it happens to land in.
func TestARevocationBelowAMountIsHonoured(t *testing.T) {
	bob := who(1)
	table := served(t, ns.Mount{Path: "/shared", Archetype: "files", Access: ns.Access{AnyPaired: true}})
	table.Granted(denying{at: "/shared/private", who: []string{"bob"}})

	asBob := ns.Caller{ID: bob.String(), Name: "bob", Paired: true}
	if _, _, no := resolve(table, asBob, "/shared/notes"); no != nil {
		t.Fatalf("bob was refused a path nobody revoked: %v", no)
	}
	if _, _, no := resolve(table, asBob, "/shared/private/notes"); no == nil {
		t.Fatal("bob opened a session under a path he had been refused")
	}
}
