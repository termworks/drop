//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// A namespace two machines hold, taken up by the second because the first one's rule names it.
//
// Nobody is invited and nothing is handed over: it turns up in `drop path ls` because the access
// rule says so, and joining it is how the second machine becomes one of the machines holding it.
func TestANamespaceSeveralMachinesHold(t *testing.T) {
	alice := newNode(t, "alice", "47871")
	bob := newNode(t, "bob", "47872")

	alice.serves(`
local drop = require("drop")

drop.mount("/chat",  { type = "chat", access = "paired" })
drop.mount("/notes", { type = "chat", access = "paired", shared = true })
drop.mount("/term",  { type = "tty",  access = "paired", shell = "/bin/sh" })
`)

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

	var name string

	t.Run("the machine that made it says what it is called", func(t *testing.T) {
		out := alice.must("path", "ls")
		if name = nameIn(out, "/notes"); name == "" {
			t.Fatalf("/notes is not shared:\n%s", out)
		}
		if listed := nameIn(out, "/chat"); listed != "" {
			t.Fatalf("/chat said nothing and is shared anyway:\n%s", out)
		}
	})

	t.Run("the other machine sees that it may hold it too", func(t *testing.T) {
		out := bob.must("path", "ls", "alice")
		if !strings.Contains(out, "/notes") || !strings.Contains(out, "shared") {
			t.Fatalf("/notes is not offered as shared:\n%s", out)
		}
	})

	t.Run("and joins it, and is told what it is", func(t *testing.T) {
		out := bob.must("path", "join", "alice:/notes")
		for _, want := range []string{"chat, shared", "also held by  alice", "history"} {
			if !strings.Contains(out, want) {
				t.Fatalf("joining did not say %q:\n%s", want, out)
			}
		}
	})

	t.Run("both machines call it the same name", func(t *testing.T) {
		out := bob.must("path", "ls")
		if here := nameIn(out, "/notes"); here != name {
			t.Fatalf("alice calls it %q and bob calls it %q:\n%s", name, here, out)
		}
	})

	t.Run("joining it again writes nothing down twice", func(t *testing.T) {
		out := bob.must("path", "join", "alice:/notes")
		if !strings.Contains(out, "already held here") {
			t.Fatalf("joining twice did not say so:\n%s", out)
		}
		if listed := bob.must("path", "ls"); strings.Count(listed, "/notes") != 1 {
			t.Fatalf("/notes is here more than once:\n%s", listed)
		}
	})

	t.Run("a namespace its machine holds alone cannot be joined", func(t *testing.T) {
		out, err := bob.run("path", "join", "alice:/chat")
		if err == nil {
			t.Fatalf("a namespace nobody else holds was joined:\n%s", out)
		}
		if !strings.Contains(out, "holds alone") {
			t.Fatalf("it did not say why:\n%s", out)
		}
	})

	t.Run("and neither can a kind of namespace nobody else can hold", func(t *testing.T) {
		out, err := bob.run("path", "join", "alice:/term")
		if err == nil {
			t.Fatalf("a terminal was joined:\n%s", out)
		}
		if !strings.Contains(out, "one machine's own") {
			t.Fatalf("it did not say why:\n%s", out)
		}
	})

	t.Run("and it is still here after a restart", func(t *testing.T) {
		stopBob()

		_, said, again := bob.background("serve")
		defer again()

		waitFor(t, "bob to be ready again", 30*time.Second, func() bool {
			return strings.Contains(said.String(), "ready")
		})
		if here := nameIn(bob.must("path", "ls"), "/notes"); here != name {
			t.Fatalf("after a restart it is called %q, want %q", here, name)
		}
	})
}

// nameIn is what a listing says a path is called, and empty when it says the path is not shared.
func nameIn(listing, path string) string {
	for _, line := range strings.Split(listing, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), path+" ") {
			continue
		}
		_, rest, found := strings.Cut(line, "· shared ")
		if !found {
			return ""
		}
		return strings.TrimSpace(rest)
	}
	return ""
}
