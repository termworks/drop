//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A namespace made on the command line, reached from another machine, and gone when the command
// that made it stops.
//
// Nothing is written down here: the path exists for exactly as long as somebody is standing there
// holding it, which is the whole of what `drop path create` without --keep promises.
func TestACreatedNamespaceIsReachedAndThenIsGone(t *testing.T) {
	holder := newNode(t, "holder", "47861")
	walker := newNode(t, "walker", "47862")

	work := filepath.Join(holder.home, "made")
	writeAt(t, filepath.Join(work, "note.txt"), "the note\n")

	shared := `
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`
	holder.serves(shared)
	walker.serves(shared)

	_, holderSaid, stopHolder := holder.background("serve")
	defer stopHolder()
	_, walkerSaid, stopWalker := walker.background("serve")
	defer stopWalker()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(holderSaid.String(), "ready") && strings.Contains(walkerSaid.String(), "ready")
	})
	pair(t, holder, walker)

	_, madeSaid, stopMade := holder.background("path", "create", "/made", "files",
		"--set", "dir="+work, "--access", "paired")

	waitFor(t, "the namespace to be up", 30*time.Second, func() bool {
		return strings.Contains(madeSaid.String(), "waiting")
	})

	t.Run("the other machine sees it", func(t *testing.T) {
		out := walker.must("path", "ls", "holder")
		if !strings.Contains(out, "/made") {
			t.Fatalf("/made is not listed:\n%s", out)
		}
	})

	t.Run("and opens it, with the settings it was created with", func(t *testing.T) {
		out := walker.must("file", "ls", "holder:/made")
		if !strings.Contains(out, "note.txt") {
			t.Fatalf("the directory it was pointed at is not there:\n%s", out)
		}
	})

	stopMade()

	t.Run("and once the command stops it is gone", func(t *testing.T) {
		waitFor(t, "the namespace to go", 30*time.Second, func() bool {
			out := walker.must("path", "ls", "holder")
			return !strings.Contains(out, "/made")
		})

		if said, err := walker.run("file", "ls", "holder:/made", "--wait", "10s"); err == nil {
			t.Fatalf("a namespace that is gone answered:\n%s", said)
		}
	})
}

// The same namespace written down, so that it is there again after the node it belongs to has been
// restarted. Nothing polls the file: it is read once, when the node starts.
func TestAKeptNamespaceIsThereAfterARestart(t *testing.T) {
	holder := newNode(t, "holder", "47863")
	walker := newNode(t, "walker", "47864")

	work := filepath.Join(holder.home, "kept")
	writeAt(t, filepath.Join(work, "note.txt"), "the note\n")

	shared := `
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`
	holder.serves(shared)
	walker.serves(shared)

	// Written down before anything is serving, which is the ordinary case: the command says so
	// rather than failing.
	said := holder.must("path", "create", "/kept", "files", "--set", "dir="+work, "--access", "paired", "--keep")
	if !strings.Contains(said, "written down") {
		t.Fatalf("it did not say it was written down:\n%s", said)
	}
	if !strings.Contains(said, "drop serve") {
		t.Fatalf("it did not say nothing was serving:\n%s", said)
	}

	_, holderSaid, stopHolder := holder.background("serve")
	defer stopHolder()
	_, walkerSaid, stopWalker := walker.background("serve")
	defer stopWalker()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(holderSaid.String(), "ready") && strings.Contains(walkerSaid.String(), "ready")
	})
	pair(t, holder, walker)

	// The node that started afterwards is serving it, and says where it came from.
	if !strings.Contains(holderSaid.String(), "written") {
		t.Fatalf("the node does not say the path was written down:\n%s", holderSaid.String())
	}

	out := walker.must("file", "ls", "holder:/kept")
	if !strings.Contains(out, "note.txt") {
		t.Fatalf("a namespace that was written down is not being served:\n%s", out)
	}

	// And taken off the list again, which the node already running is told nothing about.
	said = holder.must("path", "rm", "/kept")
	if !strings.Contains(said, "until it restarts") {
		t.Fatalf("rm did not say the running node keeps serving it:\n%s", said)
	}
}
