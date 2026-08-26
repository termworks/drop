//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A message to a device that is not there is queued, and a queued message is only worth anything if
// something eventually sends it. The thing that is always running is the daemon, so the daemon is
// what has to notice the far end coming back.
func TestAMessageWaitsForADeviceToComeBack(t *testing.T) {
	alpha := newNode(t, "alpha", "47811")
	beta := newNode(t, "beta", "47812")

	shared := `
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`
	alpha.serves(shared)
	beta.serves(shared)

	// Paired while both are up, then beta goes away.
	_, betaSaid, stopBeta := beta.background("serve")
	_, alphaSaid, stopAlpha := alpha.background("serve")
	defer stopAlpha()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(betaSaid.String(), "ready") && strings.Contains(alphaSaid.String(), "ready")
	})
	pair(t, beta, alpha)
	stopBeta()

	// Nothing is listening now, so this cannot be delivered.
	out := alpha.must("to", "beta/chat", "said while you were out")
	if !strings.Contains(out, "queued") {
		t.Fatalf("a message to a device that is off was not queued:\n%s", out)
	}

	// beta comes back, and nobody types anything else.
	_, betaAgain, stopAgain := beta.background("serve")
	defer stopAgain()

	waitFor(t, "beta to be ready again", 30*time.Second, func() bool {
		return strings.Contains(betaAgain.String(), "ready")
	})

	waitFor(t, "the backlog to go out", 90*time.Second, func() bool {
		return strings.Contains(beta.must("log", "alpha"), "said while you were out")
	})
}

// A terminal shared with `drop cast` is watched at <peer>/cast, while the daemon is running.
//
// That last part is the whole test. A cast used to stand up a node of its own, which cannot have
// the address the daemon already holds, so a watcher dialling the address in its address book
// reached the daemon and was told there was nothing there.
func TestATerminalIsCastAndWatched(t *testing.T) {
	watcher := newNode(t, "watcher", "47821")
	casting := newNode(t, "casting", "47822")

	shared := `
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`
	watcher.serves(shared)
	casting.serves(shared)

	_, castingSaid, stopCasting := casting.background("serve")
	defer stopCasting()
	_, watcherSaid, stopWatcher := watcher.background("serve")
	defer stopWatcher()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(castingSaid.String(), "ready") && strings.Contains(watcherSaid.String(), "ready")
	})
	pair(t, casting, watcher)

	// Nothing is being cast yet, so there is nothing to watch and it says so.
	early, stopEarly := context.WithTimeout(context.Background(), 30*time.Second)
	said, _ := watcher.runIn(early, "", "to", "casting/cast", "--wait", "10s")
	stopEarly()
	if strings.Contains(said, "hello from a recorded terminal") {
		t.Fatalf("something was watchable before anything was cast:\n%s", said)
	}

	// asciicast v2: a header, then timed writes. Standard input is held open, because a cast lasts
	// exactly as long as whatever is being recorded.
	into, _, stopCast := casting.backgroundWriting("cast", "--address-file", casting.home+"/cast-address")
	defer stopCast()

	for _, line := range []string{
		`{"version": 2, "width": 80, "height": 24}`,
		`[0.1, "o", "hello from a recorded terminal\r\n"]`,
		`[0.2, "o", "second line\r\n"]`,
	} {
		if _, err := io.WriteString(into, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}

	waitFor(t, "the daemon to take the cast", 30*time.Second, func() bool {
		return strings.Contains(castingSaid.String(), "being cast")
	})

	var out string
	waitFor(t, "the cast to be watchable", 60*time.Second, func() bool {
		read, stopRead := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopRead()

		said, _ := watcher.runIn(read, "", "to", "casting/cast", "--wait", "10s")
		out = said
		return strings.Contains(said, "hello from a recorded terminal")
	})

	// A watcher that arrives after something was written still sees it: a cast replays the screen
	// as it stands, rather than only what happens next.
	if !strings.Contains(out, "second line") {
		t.Errorf("a watcher did not get what was written before it arrived:\n%q", out)
	}

	// And the daemon goes on being a daemon while all this happens.
	if out := watcher.must("ls", "casting"); !strings.Contains(out, "/chat") {
		t.Errorf("the node stopped serving its own namespaces while casting:\n%s", out)
	}

	// When the cast ends, the path goes with it rather than answering with nothing behind it.
	stopCast()
	waitFor(t, "the cast to end", 30*time.Second, func() bool {
		return strings.Contains(castingSaid.String(), "ended")
	})

	if out := watcher.must("ls", "casting"); strings.Contains(out, "/cast") {
		t.Errorf("the cast path outlived the cast:\n%s", out)
	}
}

// With no daemon running there is nothing to hand the cast to, so it stands up a node of its own.
// That is how a machine that only ever shares a terminal works, and it has to keep working.
func TestACastWithoutADaemonServesItself(t *testing.T) {
	watcher := newNode(t, "watcher", "47831")
	casting := newNode(t, "casting", "47832")

	shared := `
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`
	watcher.serves(shared)
	casting.serves(shared)

	// Paired with both up, then the casting node's daemon goes away for good.
	_, castingSaid, stopCasting := casting.background("serve")
	_, watcherSaid, stopWatcher := watcher.background("serve")
	defer stopWatcher()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(castingSaid.String(), "ready") && strings.Contains(watcherSaid.String(), "ready")
	})
	pair(t, casting, watcher)
	stopCasting()

	into, castSaid, stopCast := casting.backgroundWriting("cast", "--address-file", casting.home+"/cast-address")
	defer stopCast()

	for _, line := range []string{
		`{"version": 2, "width": 80, "height": 24}`,
		`[0.1, "o", "cast on its own\r\n"]`,
	} {
		if _, err := io.WriteString(into, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}

	waitFor(t, "the cast to start", 30*time.Second, func() bool {
		return strings.Contains(castSaid.String(), "casting")
	})

	waitFor(t, "the cast to be watchable", 60*time.Second, func() bool {
		read, stopRead := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopRead()

		said, _ := watcher.runIn(read, "", "to", "casting/cast", "--wait", "10s")
		return strings.Contains(said, "cast on its own")
	})
}

// Finding a device is the expensive part. Once it has answered, the address that worked is written
// down, so the next conversation is a dial rather than a search.
func TestTheAddressThatWorkedIsRemembered(t *testing.T) {
	alpha := newNode(t, "alpha", "47841")
	beta := newNode(t, "beta", "47842")

	shared := `
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`
	alpha.serves(shared)
	beta.serves(shared)

	_, betaSaid, stopBeta := beta.background("serve")
	defer stopBeta()
	_, alphaSaid, stopAlpha := alpha.background("serve")
	defer stopAlpha()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(betaSaid.String(), "ready") && strings.Contains(alphaSaid.String(), "ready")
	})
	pair(t, beta, alpha)

	// Whatever it was reached at is written down, and it has to be an address rather than nothing.
	alpha.must("ls", "beta")

	book := filepath.Join(alpha.home, "config", "drop", "peers.json")
	waitFor(t, "the address to be written down", 30*time.Second, func() bool {
		raw, err := os.ReadFile(book)
		return err == nil && strings.Contains(string(raw), ":"+beta.port)
	})

	raw, err := os.ReadFile(book)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "addrs") {
		t.Fatalf("nothing was remembered about how to reach beta:\n%s", raw)
	}
}

// Pairing is with a person, not only with a machine.
//
// Each node generates a user key on first run and signs a badge for itself. Pairing carries that
// badge, and what each side writes down is the other's user key — which is what lets a machine of
// theirs that this one has never met be recognised later without pairing again.
func TestPairingLearnsWhoTheOtherMachineBelongsTo(t *testing.T) {
	one, two := newNode(t, "one", "45031"), newNode(t, "two", "45032")
	one.serves(`local drop = require("drop")`)
	two.serves(`local drop = require("drop")`)

	// Each side has a user, and the two are different people.
	keys := map[string]string{}
	for _, n := range []*node{one, two} {
		said := n.must("user")
		if !strings.Contains(said, "ssh-ed25519 ") {
			t.Fatalf("%s has no user key:\n%s", n.name, said)
		}
		keys[n.name] = between(t, said, "identity ", "\n")
	}
	if keys["one"] == keys["two"] {
		t.Fatal("both nodes generated the same user key")
	}

	pair(t, one, two)

	// Each address book holds the other's user key, alongside the machine it was learnt from.
	for _, side := range []struct {
		node *node
		want string
	}{{one, keys["two"]}, {two, keys["one"]}} {
		raw, err := os.ReadFile(filepath.Join(side.node.home, "config", "drop", "peers.json"))
		if err != nil {
			t.Fatalf("%s: %v", side.node.name, err)
		}
		if !strings.Contains(string(raw), side.want) {
			t.Errorf("%s did not learn the other's user key:\n%s", side.node.name, raw)
		}
	}
}

// between pulls the text between two markers out of what a command printed.
func between(t *testing.T, said, from, to string) string {
	t.Helper()

	_, rest, found := strings.Cut(said, from)
	if !found {
		t.Fatalf("nothing said %q:\n%s", from, said)
	}
	out, _, found := strings.Cut(rest, to)
	if !found {
		t.Fatalf("nothing closed %q:\n%s", from, said)
	}
	return strings.TrimSpace(out)
}

// Revoking somebody takes effect on the next connection, against a rule that was written by hand.
//
// This is the half of revocation that has an answer: this machine stops trusting them now. The
// other half — telling anybody else — has none without a server, and drop does not pretend it does.
func TestRevokingShutsAPeerOutOfAPath(t *testing.T) {
	one, two := newNode(t, "one", "45041"), newNode(t, "two", "45042")
	one.serves(`
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`)
	two.serves(`
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`)

	// Paired before anything is serving: `drop pair` starts an endpoint of its own, and it cannot
	// have the port while a daemon on the same node is holding it.
	pair(t, one, two)

	_, _, stop := one.background("serve")
	defer stop()

	// A message gets through while nothing has been revoked.
	two.must("to", "one/chat", "before")
	waitFor(t, "the first message", 30*time.Second, func() bool {
		return strings.Contains(one.must("log", "two"), "before")
	})

	one.must("revoke", "/chat", "two")

	// The far end refuses it now, and says so rather than swallowing it. What it says is the same
	// thing it says about a path that is not there at all, so being turned away teaches nothing
	// about what this device serves.
	said, err := two.run("to", "one/chat", "after")
	if err == nil {
		t.Fatalf("a revoked device was still admitted:\n%s", said)
	}
	if !strings.Contains(said, "not shared with you") {
		t.Errorf("the refusal was not explained:\n%s", said)
	}
	if strings.Contains(said, "queued") {
		t.Errorf("a refused message was queued to be retried forever:\n%s", said)
	}
	if got := one.must("log", "two"); strings.Contains(got, "after") {
		t.Errorf("a message from a revoked device was stored:\n%s", got)
	}

	// And lifting it lets them back in, without restarting anything.
	one.must("revoke", "/chat", "two", "--forget")
	two.must("to", "one/chat", "again")
	waitFor(t, "the message after the refusal was lifted", 30*time.Second, func() bool {
		return strings.Contains(one.must("log", "two"), "again")
	})
}

// A vaulted node keeps its history unreadable on disk, and reads it back itself.
//
// The wire is not the point here: what a peer sees is the same either way. The point is the disk —
// a stolen laptop, a pulled disk, a leaked backup — and the test for that is `strings`.
func TestAVaultedHistoryIsNotReadableOnDisk(t *testing.T) {
	one, two := newNode(t, "one", "45051"), newNode(t, "two", "45052")

	one.serves(`
local drop = require("drop")
drop.vault = "` + filepath.Join(one.home, "config", "drop", "vault.key") + `"
drop.mount("/chat", { type = "chat", access = "paired" })
`)
	two.serves(`
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`)

	pair(t, one, two)

	_, _, stop := one.background("serve")
	defer stop()

	two.must("to", "one/chat", "the eagle has landed")
	waitFor(t, "the message to arrive", 30*time.Second, func() bool {
		return strings.Contains(one.must("log", "two"), "the eagle has landed")
	})

	// Written, read back by drop, and not there for anybody with the disk.
	convo := filepath.Join(one.home, "data", "drop", "convo")
	found := false
	err := filepath.Walk(convo, func(at string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(at)
		if err != nil {
			return err
		}
		found = true
		if strings.Contains(string(raw), "the eagle has landed") {
			t.Errorf("%s holds the message in the clear", at)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("nothing was written to disk at all")
	}

	// And the vault says what it is doing.
	if said := one.must("vault"); !strings.Contains(said, "open") {
		t.Errorf("the vault does not report itself open:\n%s", said)
	}
}

// A conversation written in the clear can be sealed afterwards, and put back.
func TestWhatIsAlreadyOnDiskCanBeSealed(t *testing.T) {
	one, two := newNode(t, "one", "45061"), newNode(t, "two", "45062")

	plain := `
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`
	one.serves(plain)
	two.serves(plain)

	pair(t, one, two)

	stopped := func() {}
	_, _, stopped = one.background("serve")

	two.must("to", "one/chat", "written in the clear")
	waitFor(t, "the message to arrive", 30*time.Second, func() bool {
		return strings.Contains(one.must("log", "two"), "written in the clear")
	})

	// The daemon has to be out of the way: a message landing during the walk is in neither file.
	stopped()

	onDisk := func() string {
		t.Helper()

		var all strings.Builder
		err := filepath.Walk(filepath.Join(one.home, "data", "drop", "convo"),
			func(at string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return err
				}
				raw, err := os.ReadFile(at)
				all.Write(raw)
				return err
			})
		if err != nil {
			t.Fatal(err)
		}
		return all.String()
	}
	if !strings.Contains(onDisk(), "written in the clear") {
		t.Fatal("the message was not on disk in the clear to begin with")
	}

	// A vault, and then the walk.
	one.serves(plain + `
drop.vault = "` + filepath.Join(one.home, "config", "drop", "vault.key") + `"
`)
	if said := one.must("vault", "seal"); !strings.Contains(said, "conversation") {
		t.Errorf("sealing said nothing useful:\n%s", said)
	}

	if strings.Contains(onDisk(), "written in the clear") {
		t.Error("the message is still on disk in the clear")
	}
	if got := one.must("log", "two"); !strings.Contains(got, "written in the clear") {
		t.Errorf("drop cannot read its own sealed history:\n%s", got)
	}

	// And back again.
	one.must("vault", "clear")
	if !strings.Contains(onDisk(), "written in the clear") {
		t.Error("clearing did not put the message back")
	}
}

// Pairing with a machine rather than a person is a deliberate refusal of transitive trust: the
// device key is kept and the user key is not, so the rest of that person's machines stay strangers
// however many badges they sign.
func TestPairingWithAMachineLearnsNoPerson(t *testing.T) {
	one, two := newNode(t, "one", "45071"), newNode(t, "two", "45072")
	one.serves(`local drop = require("drop")`)
	two.serves(`local drop = require("drop")`)

	pairing(t, one, two, "--machine")

	// The side that asked for a machine kept no user key.
	raw, err := os.ReadFile(filepath.Join(two.home, "config", "drop", "peers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\"user\"") {
		t.Errorf("a machine-level pairing learnt a person:\n%s", raw)
	}

	// And the other side, which was not asked anything, still did.
	raw, err = os.ReadFile(filepath.Join(one.home, "config", "drop", "peers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\"user\"") {
		t.Errorf("the other side lost its person too:\n%s", raw)
	}
}

// A device that dialled and was refused is written down, so letting a bare id in later does not
// mean copying sixty-four characters of hex out of a log.
func TestAStrangerThatDialledIsWrittenDown(t *testing.T) {
	host, stranger := newNode(t, "host", "45081"), newNode(t, "stranger", "45082")

	host.serves(`
local drop = require("drop")
drop.mount("/work", { type = "chat", access = "paired" })
`)
	stranger.serves(`
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`)

	_, _, stop := host.background("serve")
	defer stop()

	id := strings.TrimSpace(host.must("id"))

	// Not paired with anybody, so this is refused — and that is the point.
	if said, err := stranger.run("to", id+"/work", "let me in"); err == nil {
		t.Fatalf("a stranger was admitted:\n%s", said)
	}

	at := filepath.Join(host.home, "data", "drop", "seen.json")
	waitFor(t, "the knock to be written down", 30*time.Second, func() bool {
		_, err := os.Stat(at)
		return err == nil
	})

	raw, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), strings.TrimSpace(stranger.must("id"))) {
		t.Errorf("the stranger's id was not written down:\n%s", raw)
	}
	if !strings.Contains(string(raw), "/work") {
		t.Errorf("what it asked for was not written down:\n%s", raw)
	}
}

// Two people on one machine.
//
// A profile is not a second name for the same identity: it gets its own device key, its own user
// key, its own address book and its own conversations. That is what makes it possible to try a rule
// that names somebody else without owning a second computer.
func TestTwoProfilesOnOneMachineAreStrangers(t *testing.T) {
	me := newNode(t, "me", "45091")

	// Bob lives under the same home, reached only by setting DROP_PROFILE.
	bob := &node{t: t, name: "bob", home: me.home, port: "", profile: "bob"}

	shared := `
local drop = require("drop")
drop.mount("/chat",   { type = "chat", access = "paired" })
drop.mount("/private", { type = "chat", access = { "me" } })
`
	me.serves(shared)
	bob.serves(shared)

	// Different user keys, so different people.
	mine := between(t, me.must("user"), "identity ", "\n")
	his := between(t, bob.must("user"), "identity ", "\n")
	if mine == his {
		t.Fatal("a profile shares the ordinary identity")
	}

	// Different devices too.
	if strings.TrimSpace(me.must("id")) == strings.TrimSpace(bob.must("id")) {
		t.Fatal("a profile shares the ordinary device key")
	}

	pair(t, me, bob)

	_, _, stop := me.background("serve")
	defer stop()

	// Paired, so the shared path works.
	bob.must("to", "me/chat", "hello from somebody else")
	waitFor(t, "the message", 30*time.Second, func() bool {
		return strings.Contains(me.must("log"), "hello from somebody else")
	})

	// But a path for my own machines refuses him, which is the whole point.
	if said, err := bob.run("to", "me/private", "and this"); err == nil {
		t.Fatalf("a different person reached a path meant for my own machines:\n%s", said)
	}
}

// Pairing with the local wire turned off.
//
// mDNS reaches the same wire and nothing else, so with it off a device can only be found the way a
// device on somebody else's network is found: it publishes where it is, and the other side resolves
// that and dials it back through a relay. This is the path that matters and the one nothing else
// here exercises -- every other test pairs over the wire without knowing it.
func TestPairingWorksWithTheLocalWireOff(t *testing.T) {
	me := newNode(t, "me", "45101")
	me.blind = true

	them := &node{t: t, name: "them", home: me.home, port: "", profile: "them", blind: true}

	shared := `
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`
	me.serves(shared)
	them.serves(shared)

	// Serving, so the offer is published by the daemon the way it is in real use.
	_, said, stop := me.background("serve")
	defer stop()
	waitFor(t, "the daemon", 30*time.Second, func() bool {
		return strings.Contains(said.String(), "ready")
	})

	_, offered, stopOffer := me.background("pair", "--code", "no-mdns-test", "--wait", "3m")
	defer stopOffer()

	var ticket string
	waitFor(t, "a ticket", 30*time.Second, func() bool {
		ticket = ticketIn(offered.String())
		return ticket != ""
	})

	// The wire really is off on the offering side, or this test proves nothing. The offer is made
	// through the daemon, so it is the daemon that says so.
	if !strings.Contains(said.String(), "mDNS unavailable") {
		t.Fatalf("the local wire was still up on the offering side:\n%s", said.String())
	}

	// No --at, no address of any kind: only the id in the ticket.
	out, err := them.run("pair", ticket)
	if err != nil {
		t.Fatalf("pairing without the local wire failed:\n%s", out)
	}
	if !strings.Contains(out, "mDNS unavailable") {
		t.Fatalf("the local wire was still up on the joining side:\n%s", out)
	}
	if !strings.Contains(out, "reach the other") {
		t.Fatalf("pairing did not finish:\n%s", out)
	}

	// And it can be used afterwards, which is the point of having paired.
	them.must("to", "me/chat", "found you the long way round")
	waitFor(t, "the message", 60*time.Second, func() bool {
		return strings.Contains(me.must("log"), "found you the long way round")
	})
}

// A device paired with while something is already serving must be answered properly straight away.
//
// The address book is on disk and read into memory once. Pairing is a separate write to that file —
// sometimes from another process, sometimes from the very interface that is serving — so whatever
// answers has to re-read before it decides who somebody is. Without that, a device that has just
// paired is told the far end shares nothing, which looks exactly like pairing having failed.
func TestADeviceThatJustPairedIsAnsweredAtOnce(t *testing.T) {
	me := newNode(t, "me", "45111")
	them := newNode(t, "them", "45112")

	shared := `
local drop = require("drop")
drop.mount("/chat",  { type = "chat", access = "paired" })
drop.mount("/inbox", { type = "share", access = "paired", dir = "%s" })
`
	me.serves(fmt.Sprintf(shared, me.inbox()))
	them.serves(fmt.Sprintf(shared, them.inbox()))

	// Both serving before they have ever met, which is the case that goes wrong.
	_, mine, stopMe := me.background("serve")
	defer stopMe()
	_, theirs, stopThem := them.background("serve")
	defer stopThem()

	waitFor(t, "both daemons", 30*time.Second, func() bool {
		return strings.Contains(mine.String(), "ready") && strings.Contains(theirs.String(), "ready")
	})

	pair(t, me, them)

	// No restart of anything: what the far end shares has to be visible now.
	waitFor(t, "the paths to show up", 30*time.Second, func() bool {
		return strings.Contains(them.must("ls", "me"), "/chat")
	})

	if said := them.must("ls", "me"); !strings.Contains(said, "/inbox") {
		t.Errorf("a device that just paired was told too little:\n%s", said)
	}
}

// A path that is visible but not shared: it shows up, refuses to open, and can be asked for.
//
// The rung between shared and secret. A folder made later appears for the people it is meant for
// and they ask for it, so nobody has to paste a path around and nobody who was not meant to see it
// learns that it exists.
func TestAVisiblePathIsSeenAskedForAndGranted(t *testing.T) {
	me := newNode(t, "me", "45121")
	them := newNode(t, "them", "45122")

	me.serves(`
local drop = require("drop")
drop.mount("/chat",   { type = "chat", access = "paired" })
drop.mount("/vault",  { type = "chat", visible = "paired" })
drop.mount("/hidden", { type = "chat" })
`)
	them.serves(`
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`)

	pair(t, me, them)

	_, _, stop := me.background("serve")
	defer stop()

	// Seen, and said to be locked. The one nothing mentions is not seen at all.
	said := them.must("ls", "me")
	if !strings.Contains(said, "/vault") {
		t.Fatalf("a visible path was not listed:\n%s", said)
	}
	if !strings.Contains(said, "/chat") {
		t.Fatalf("the shared path went missing:\n%s", said)
	}
	if strings.Contains(said, "/hidden") {
		t.Fatalf("a path shared with nobody was listed:\n%s", said)
	}

	// Seen is not open.
	if out, err := them.run("to", "me/vault", "let me in"); err == nil {
		t.Fatalf("a visible path was opened:\n%s", out)
	} else if !strings.Contains(out, "ask") {
		t.Errorf("the refusal did not say it could be asked for:\n%s", out)
	}

	// Ringing the bell.
	if out := them.must("ask", "me/vault", "--why", "for the thing we discussed"); !strings.Contains(out, "asked") {
		t.Fatalf("asking said nothing useful:\n%s", out)
	}

	waitFor(t, "the request to be written down", 30*time.Second, func() bool {
		return strings.Contains(me.must("requests"), "/vault")
	})
	if got := me.must("requests"); !strings.Contains(got, "for the thing we discussed") {
		t.Errorf("what they said about it was lost:\n%s", got)
	}

	// Answering it is a grant, and it takes effect on the next connection.
	me.must("requests", "allow", "/vault", "them")

	waitFor(t, "the path to open", 30*time.Second, func() bool {
		_, err := them.run("to", "me/vault", "thank you")
		return err == nil
	})

	// And the request is off the list, because it has been dealt with.
	if got := me.must("requests"); strings.Contains(got, "/vault") {
		t.Errorf("an answered request is still pending:\n%s", got)
	}
}

// A second drop on one machine cannot have the identity's port, and has to say so.
//
// Silence here is expensive: the second process starts, answers nothing anybody dials, and the
// first one keeps replying with whatever version it happens to be running. From the outside that
// looks like the device answering strangely rather than like two processes.
func TestASecondDaemonSaysItCannotBeReached(t *testing.T) {
	one := newNode(t, "one", "45131")
	one.serves(`
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`)

	_, first, stopFirst := one.background("serve")
	defer stopFirst()
	waitFor(t, "the first daemon", 30*time.Second, func() bool {
		return strings.Contains(first.String(), "ready")
	})
	if strings.Contains(first.String(), "holds this identity") {
		t.Fatalf("the first daemon complained about itself:\n%s", first.String())
	}

	// The same machine, the same identity, the same port.
	_, second, stopSecond := one.background("serve")
	defer stopSecond()

	waitFor(t, "the second daemon to say so", 30*time.Second, func() bool {
		return strings.Contains(second.String(), "holds this identity")
	})
	if !strings.Contains(second.String(), "pkill") {
		t.Errorf("it did not say what to do about it:\n%s", second.String())
	}
}

// A device nothing can dial still gets what is queued for it, over the connection it opened.
//
// This is the ordinary case behind a NAT: one side can open connections and the other cannot reach
// it at all. The unreachable side holds a connection open to everybody it has paired with, and
// whatever is waiting goes down that pipe — without the waiting side ever dialling.
func TestAQueueEmptiesOverTheConnectionThePeerOpened(t *testing.T) {
	me := newNode(t, "me", "45141")
	them := newNode(t, "them", "45142")

	me.serves(`
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`)

	them.serves(`
local drop = require("drop")
drop.mount("/chat", { type = "chat", access = "paired" })
`)

	pair(t, me, them)

	// From here on they are unfindable: no local wire, and they publish nowhere. They can still
	// find everybody else and dial out, which is exactly what a device behind a strict NAT can do.
	them.blind = true

	_, said, stopMe := me.background("serve")
	defer stopMe()
	waitFor(t, "my daemon", 30*time.Second, func() bool {
		return strings.Contains(said.String(), "ready")
	})

	// Written down while they are not even running.
	me.must("to", "them/chat", "waiting for you to say something")
	if got := them.must("log"); strings.Contains(got, "waiting for you") {
		t.Fatal("it arrived before the far end existed")
	}

	// They come up. Nothing can find them — but they can find us, and they hold that connection.
	_, theirs, stopThem := them.background("serve")
	defer stopThem()
	waitFor(t, "their daemon", 30*time.Second, func() bool {
		return strings.Contains(theirs.String(), "ready")
	})

	waitFor(t, "the queue to empty over their connection", 90*time.Second, func() bool {
		return strings.Contains(them.must("log"), "waiting for you to say something")
	})
}
