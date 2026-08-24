//go:build e2e

package e2e

import (
	"context"
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
