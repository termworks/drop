//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A kind of namespace one machine has and the other does not.
//
// The archetype is a file on alice's disk and nowhere else. Bob's drop is the same binary and has
// never heard the word camera, and he still opens it — because the camera says which protocol it
// speaks, and that is the whole of what a caller ever needed to know about it. The lens beside it
// says nothing, and bob is told plainly why he cannot have it.

// theCamera and the lens are the whole of what alice was given.
const theCamera = `
drop.archetype{
  name  = "camera",
  shape = "note",
  read  = function(d)
    if not d.device then error("a camera namespace needs a device") end
    return { device = d.device }
  end,
  note  = function(c)
    return { detail = c.device, about = "a camera, as it sees things", glyph = "◉" }
  end,
  serve = function(s, c) s:write("looking at " .. c.device .. "\n") end,
}

drop.archetype{
  name  = "lens",
  read  = function(d) return {} end,
  note  = function(c) return { about = "a lens, which speaks only to itself" } end,
  serve = function(s, c) s:write("nobody can read this") end,
}
`

// declares writes an archetype beside a node's config, which is where drop looks for the ones
// nobody compiled.
func (n *node) declares(name, source string) {
	n.t.Helper()

	dir := filepath.Join(n.home, "config", "drop", "archetypes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		n.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
		n.t.Fatal(err)
	}
}

func TestAnArchetypeWrittenInLuaIsOpenedByAMachineWithoutIt(t *testing.T) {
	alice := newNode(t, "alice", "47901")
	bob := newNode(t, "bob", "47902")

	alice.declares("camera.lua", theCamera)
	alice.serves(`
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
drop.mount("/film", { type = "camera", device = "/dev/video0", access = "paired" })
drop.mount("/lens", { type = "lens", access = "paired" })
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

	t.Run("bob's listing names a kind of namespace his build has never heard of", func(t *testing.T) {
		out := bob.must("path", "ls", "alice:")
		for _, want := range []string{"/film", "camera", "a camera, as it sees things"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%q is not in the listing:\n%s", want, out)
			}
		}
	})

	t.Run("and he opens it as the archetype it says it speaks", func(t *testing.T) {
		out := bob.must("connect", "alice:/film")
		if !strings.Contains(out, "looking at /dev/video0") {
			t.Fatalf("connecting to the camera printed:\n%s", out)
		}
	})

	t.Run("while one that says nothing is refused, and says why", func(t *testing.T) {
		out, err := bob.run("connect", "alice:/lens")
		if err == nil {
			t.Fatalf("a lens was opened by a machine that has no lens:\n%s", out)
		}
		for _, want := range []string{"lens", "lua"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%q is not in the refusal:\n%s", want, out)
			}
		}
	})
}
