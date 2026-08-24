//go:build e2e

package e2e

import (
	"context"
	"io"
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

// A terminal shared with `drop cast` is watched at <peer>/cast. It runs as its own node rather than
// through the daemon, which is how a program being recorded hands its output over.
func TestATerminalIsCastAndWatched(t *testing.T) {
	watcher := newNode(t, "watcher", "47821")
	casting := newNode(t, "casting", "47822")

	watcher.serves(`
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`)
	casting.serves(`
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`)

	_, castingSaid, stopServe := casting.background("serve")
	_, watcherSaid, stopWatcher := watcher.background("serve")
	defer stopWatcher()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(castingSaid.String(), "ready") && strings.Contains(watcherSaid.String(), "ready")
	})
	pair(t, casting, watcher)

	// The daemon is stopped so the cast owns the node's port: a cast is its own listener, and two
	// on one identity is a race for whoever dials.
	stopServe()

	// asciicast v2: a header, then timed writes. Standard input is held open, because a cast lasts
	// exactly as long as whatever is being recorded.
	into, castSaid, stopCast := casting.backgroundWriting("cast", "--address-file", casting.home+"/cast-address")
	defer stopCast()

	if _, err := io.WriteString(into, `{"version": 2, "width": 80, "height": 24}`+"\n"); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`[0.1, "o", "hello from a recorded terminal\r\n"]`,
		`[0.2, "o", "second line\r\n"]`,
	} {
		if _, err := io.WriteString(into, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}

	waitFor(t, "the cast to start", 30*time.Second, func() bool {
		return strings.Contains(castSaid.String(), casting.home) || strings.Contains(castSaid.String(), "watch")
	})

	// The watcher reads it as any other live path.
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
}
