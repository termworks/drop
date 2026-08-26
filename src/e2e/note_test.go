//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// One file, two people, and no moment at which they could see each other change it.
//
// This is the thing the whole shape is for: a note held on two machines, a change made on each
// while both are down, and the two files identical afterwards with both edits in them. Nobody is
// asked which version to keep, because neither person touched what the other did.
func TestANoteTwoPeopleEditAtOnce(t *testing.T) {
	alice := newNode(t, "alice", "47881")
	bob := newNode(t, "bob", "47882")

	hers := filepath.Join(alice.home, "notes.md")
	his := filepath.Join(bob.home, "notes.md")

	alice.serves(fmt.Sprintf(`
local drop = require("drop")

drop.mount("/chat",  { type = "chat", access = "paired" })
drop.mount("/notes", { type = "note", access = "paired", shared = true, file = %q })
`, hers))

	bob.serves(`
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`)

	_, aliceSaid, stopAlice := alice.background("serve")
	defer stopAlice()
	_, bobSaid, stopBob := bob.background("serve")
	defer stopBob()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(aliceSaid.String(), "ready") && strings.Contains(bobSaid.String(), "ready")
	})
	pair(t, alice, bob)

	const first = "one\ntwo\nthree\nfour\nfive\n"

	t.Run("what alice writes in her own editor becomes the note", func(t *testing.T) {
		writing(t, hers, first)

		out := alice.must("path", "ls")
		if !strings.Contains(out, "/notes") || !strings.Contains(out, "note") {
			t.Fatalf("/notes is not served as a note:\n%s", out)
		}
	})

	t.Run("bob joins it and the file appears on his disk", func(t *testing.T) {
		out := bob.must("path", "join", "alice:/notes", "--set", "file="+his)
		if !strings.Contains(out, "note, shared") {
			t.Fatalf("joining did not say what it is:\n%s", out)
		}

		waitFor(t, "the note to reach bob", 60*time.Second, func() bool {
			return reading(t, his) == first
		})
	})

	t.Run("and reading it without holding it says the same thing", func(t *testing.T) {
		out := bob.must("connect", "alice:/notes")
		if !strings.Contains(out, "three") {
			t.Fatalf("connecting to the note printed:\n%s", out)
		}
	})

	t.Run("each edits it while neither can reach the other", func(t *testing.T) {
		stopAlice()
		stopBob()

		writing(t, hers, "ONE\ntwo\nthree\nfour\nfive\n")
		writing(t, his, "one\ntwo\nthree\nfour\nFIVE\n")
	})

	t.Run("and when they meet again both files hold both edits", func(t *testing.T) {
		_, aliceAgain, stopAlice := alice.background("serve")
		defer stopAlice()
		_, bobAgain, stopBob := bob.background("serve")
		defer stopBob()

		waitFor(t, "both nodes to be ready again", 30*time.Second, func() bool {
			return strings.Contains(aliceAgain.String(), "ready") && strings.Contains(bobAgain.String(), "ready")
		})

		const both = "ONE\ntwo\nthree\nfour\nFIVE\n"
		waitFor(t, "the two notes to come out the same", 120*time.Second, func() bool {
			return reading(t, hers) == both && reading(t, his) == both
		})

		if got := reading(t, hers); got != reading(t, his) {
			t.Fatalf("alice holds %q and bob holds %q", got, reading(t, his))
		}
		if strings.Contains(reading(t, hers), "<<<<<<<") {
			t.Fatalf("nobody touched the same line and it was marked anyway:\n%s", reading(t, hers))
		}
	})
}

// writing saves a file the way a person's editor does.
func writing(t *testing.T, at, body string) {
	t.Helper()

	if err := os.WriteFile(at, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// reading is what a file holds, and nothing when it is not there yet.
func reading(t *testing.T, at string) string {
	t.Helper()

	raw, err := os.ReadFile(at)
	if err != nil {
		return ""
	}
	return string(raw)
}
