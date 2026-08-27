//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The address, end to end: whose machine, which machine, and what on it.
//
// Two real daemons, so what is being checked is that the form a person types reaches the namespace
// they meant — including the one that names a person and leaves the machine to drop.
func TestAnAddressReachesWhatItNames(t *testing.T) {
	here := newNode(t, "here", "47861")
	there := newNode(t, "there", "47862")

	work := filepath.Join(there.home, "work")
	writeAt(t, filepath.Join(work, "note.txt"), "the note\n")

	there.serves(`
local drop = require("drop")

drop.mount("/chat",  { type = "chat", access = "paired" })
drop.mount("/work",  { type = "files", access = "paired", writable = true, dir = "` + work + `" })
drop.mount("/ticks", { type = "stream", access = "paired",
  command = "sh -c 'for i in 1 2 3; do echo tick $i; done'" })
`)

	here.serves(`
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
drop.mount("/mine", { type = "files", access = "paired", dir = "` + here.home + `" })
`)

	_, thereSaid, stopThere := there.background("serve")
	defer stopThere()
	_, hereSaid, stopHere := here.background("serve")
	defer stopHere()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(thereSaid.String(), "ready") && strings.Contains(hereSaid.String(), "ready")
	})
	pair(t, there, here)

	t.Run("a machine named outright takes a message", func(t *testing.T) {
		here.must("connect", "there:/chat", "named the machine")

		waitFor(t, "the message to arrive", 30*time.Second, func() bool {
			return strings.Contains(there.must("me", "log", "here"), "named the machine")
		})
	})

	t.Run("a person with one machine needs no machine named", func(t *testing.T) {
		here.must("connect", "there::/chat", "named only the person")

		waitFor(t, "the message to arrive", 30*time.Second, func() bool {
			return strings.Contains(there.must("me", "log", "here"), "named only the person")
		})
	})

	t.Run("a stream is followed until it ends", func(t *testing.T) {
		out := here.must("connect", "there:/ticks")

		for _, want := range []string{"tick 1", "tick 2", "tick 3"} {
			if !strings.Contains(out, want) {
				t.Errorf("the stream did not carry %q:\n%s", want, out)
			}
		}
	})

	t.Run("a directory is listed, read and written", func(t *testing.T) {
		if out := here.must("file", "ls", "there:/work"); !strings.Contains(out, "note.txt") {
			t.Fatalf("the directory was not listed:\n%s", out)
		}

		here.must("file", "get", "there:/work/note.txt")
		if got := read(t, filepath.Join(here.home, "note.txt")); got != "the note\n" {
			t.Errorf("what came out is %q", got)
		}

		up := filepath.Join(t.TempDir(), "sent.txt")
		writeAt(t, up, "put over the wire\n")
		here.must("file", "put", "there:/work", up)

		if got := read(t, filepath.Join(work, "sent.txt")); got != "put over the wire\n" {
			t.Errorf("what went in is %q", got)
		}
	})

	t.Run("connecting to a machine is opening what it serves", func(t *testing.T) {
		out := here.must("connect", "there:/work")
		if !strings.Contains(out, "note.txt") {
			t.Errorf("connecting to a directory did not list it:\n%s", out)
		}
		if !strings.Contains(out, "drop file get there:/work") {
			t.Errorf("it did not say what walks it from there:\n%s", out)
		}
	})

	t.Run("an address that is this machine is served from here", func(t *testing.T) {
		bare := here.must("path", "ls")
		slash := here.must("path", "ls", "/")

		for _, out := range []string{bare, slash} {
			if !strings.Contains(out, "/mine") {
				t.Errorf("this machine did not list its own namespaces:\n%s", out)
			}
		}

		if out := here.must("file", "ls", "/mine"); !strings.Contains(out, "config/") {
			t.Errorf("this machine's own directory was not listed:\n%s", out)
		}
	})

	t.Run("and one that cannot be served from here says so", func(t *testing.T) {
		for _, refused := range [][]string{
			{"connect", "/chat", "nobody is there"},
			{"file", "get", "/mine/note.txt"},
		} {
			out, err := here.run(refused...)
			if err == nil {
				t.Errorf("drop %s was allowed:\n%s", strings.Join(refused, " "), out)
				continue
			}
			if !strings.Contains(out, "this machine") {
				t.Errorf("drop %s did not say why:\n%s", strings.Join(refused, " "), out)
			}
		}
	})

	t.Run("a namespace nobody serves is refused by the machine that has it", func(t *testing.T) {
		out, err := here.run("connect", "there:/nowhere", "let me in")
		if err == nil {
			t.Fatalf("connecting to nothing succeeded:\n%s", out)
		}
		if !strings.Contains(out, "/nowhere") {
			t.Errorf("the refusal does not say what was asked for:\n%s", out)
		}
	})
}
