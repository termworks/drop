//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A directory somebody shares, walked from the command line: what is in it, what is in the
// directory below that, one file out, one file in, and the tidying up afterwards.
//
// One test rather than several, because every step needs the pairing the step before it made.
func TestADirectoryIsWalkedFromTheCommandLine(t *testing.T) {
	walker := newNode(t, "walker", "47851")
	holder := newNode(t, "holder", "47852")

	work := filepath.Join(holder.home, "work")
	writeAt(t, filepath.Join(work, "note.txt"), "the note\n")
	writeAt(t, filepath.Join(work, "deep", "inner.txt"), "deeper\n")

	holder.serves(`
local drop = require("drop")

drop.mount("/chat",  { type = "chat",  access = "paired" })
drop.mount("/work",  { type = "files", access = "paired", dir = "` + work + `", writable = true })
drop.mount("/read",  { type = "files", access = "paired", dir = "` + work + `" })
`)
	walker.serves(`
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`)

	_, holderSaid, stopHolder := holder.background("serve")
	defer stopHolder()
	_, walkerSaid, stopWalker := walker.background("serve")
	defer stopWalker()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(holderSaid.String(), "ready") && strings.Contains(walkerSaid.String(), "ready")
	})
	pair(t, holder, walker)

	t.Run("a device still lists its namespaces", func(t *testing.T) {
		out := walker.must("ls", "holder")
		for _, path := range []string{"/chat", "/work", "/read"} {
			if !strings.Contains(out, path) {
				t.Errorf("%s is not listed:\n%s", path, out)
			}
		}
	})

	t.Run("a path that is a directory lists what is in it", func(t *testing.T) {
		out := walker.must("ls", "holder/work")
		if !strings.Contains(out, "note.txt") {
			t.Errorf("the file in it is not listed:\n%s", out)
		}
		if !strings.Contains(out, "deep/") {
			t.Errorf("the directory in it is not marked as one:\n%s", out)
		}
	})

	t.Run("and it keeps walking", func(t *testing.T) {
		out := walker.must("ls", "holder/work/deep")
		if !strings.Contains(out, "inner.txt") {
			t.Errorf("the directory below was not walked into:\n%s", out)
		}
		if strings.Contains(out, "note.txt") {
			t.Errorf("what is above came back as well:\n%s", out)
		}
	})

	t.Run("one file is copied out", func(t *testing.T) {
		into := filepath.Join(t.TempDir(), "taken.txt")
		walker.must("get", "holder/work/deep/inner.txt", into)

		if got := read(t, into); got != "deeper\n" {
			t.Errorf("what arrived is %q", got)
		}
	})

	t.Run("and lands under its own name when nowhere was said", func(t *testing.T) {
		walker.must("get", "holder/work/note.txt")

		// A command runs in the node's home, which is where a bare name lands.
		if got := read(t, filepath.Join(walker.home, "note.txt")); got != "the note\n" {
			t.Errorf("what arrived is %q", got)
		}
	})

	t.Run("files are copied in", func(t *testing.T) {
		up := filepath.Join(t.TempDir(), "sent.txt")
		writeAt(t, up, "from the walker\n")

		walker.must("put", "holder/work/deep", up)

		if got := read(t, filepath.Join(work, "deep", "sent.txt")); got != "from the walker\n" {
			t.Errorf("what landed is %q", got)
		}
	})

	t.Run("standard input is copied in under a name", func(t *testing.T) {
		if _, err := walker.runIn(within(t), "typed in\n", "put", "holder/work", "-", "--as", "typed.txt"); err != nil {
			t.Fatalf("put -: %v", err)
		}
		if got := read(t, filepath.Join(work, "typed.txt")); got != "typed in\n" {
			t.Errorf("what landed is %q", got)
		}
	})

	t.Run("a directory is made and something is moved into place", func(t *testing.T) {
		walker.must("mkdir", "holder/work/made")

		stat, err := os.Stat(filepath.Join(work, "made"))
		if err != nil || !stat.IsDir() {
			t.Fatalf("the directory was not made: %v", err)
		}

		walker.must("mv", "holder/work/typed.txt", "made/typed.txt")
		if got := read(t, filepath.Join(work, "made", "typed.txt")); got != "typed in\n" {
			t.Errorf("what was moved is %q", got)
		}
	})

	t.Run("and taken away again", func(t *testing.T) {
		walker.must("rm", "holder/work/made/typed.txt")
		walker.must("rm", "holder/work/made")

		if _, err := os.Stat(filepath.Join(work, "made")); err == nil {
			t.Error("the directory outlived the removal")
		}
	})

	t.Run("a read-only directory refuses every write", func(t *testing.T) {
		up := filepath.Join(t.TempDir(), "nope.txt")
		writeAt(t, up, "no\n")

		if out := walker.must("ls", "holder/read"); !strings.Contains(out, "note.txt") {
			t.Errorf("a read-only directory could not be listed:\n%s", out)
		}

		for _, refused := range [][]string{
			{"put", "holder/read", up},
			{"rm", "holder/read/note.txt"},
			{"mkdir", "holder/read/made"},
			{"mv", "holder/read/note.txt", "moved.txt"},
		} {
			if out, err := walker.run(refused...); err == nil {
				t.Errorf("drop %s was allowed:\n%s", strings.Join(refused, " "), out)
			}
		}
		if _, err := os.Stat(filepath.Join(work, "note.txt")); err != nil {
			t.Errorf("a read-only directory lost a file: %v", err)
		}
	})

	t.Run("nothing outside the directory is reachable", func(t *testing.T) {
		for _, leaving := range [][]string{
			{"ls", "holder/work/../.."},
			{"get", "holder/work/../../../etc/passwd", filepath.Join(t.TempDir(), "taken")},
			{"mkdir", "holder/work/../made-outside"},
			{"rm", "holder/work/../../work"},
		} {
			if out, err := walker.run(leaving...); err == nil {
				t.Errorf("drop %s was allowed:\n%s", strings.Join(leaving, " "), out)
			}
		}
	})
}

// A dropbox is not in anybody's config: it appears when somebody is waiting for a file, takes one
// transfer, and is gone.
func TestADropboxAppearsAndIsGoneAgain(t *testing.T) {
	sender := newNode(t, "sender", "47853")
	taker := newNode(t, "taker", "47854")

	shared := `
local drop = require("drop")

drop.mount("/chat", { type = "chat", access = "paired" })
`
	sender.serves(shared)
	taker.serves(shared)

	_, takerSaid, stopTaker := taker.background("serve")
	defer stopTaker()
	_, senderSaid, stopSender := sender.background("serve")
	defer stopSender()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(takerSaid.String(), "ready") && strings.Contains(senderSaid.String(), "ready")
	})
	pair(t, taker, sender)

	// Nothing is at that path until somebody asks for it.
	if out := sender.must("ls", "taker"); strings.Contains(out, "/share") {
		t.Fatalf("a dropbox was there before anybody opened one:\n%s", out)
	}

	into := filepath.Join(taker.home, "dropbox")
	if err := os.MkdirAll(into, 0o755); err != nil {
		t.Fatal(err)
	}

	_, shareSaid, stopShare := taker.background("share", into)
	defer stopShare()

	waitFor(t, "the dropbox to open", 30*time.Second, func() bool {
		return strings.Contains(takerSaid.String(), "a dropbox is open")
	})
	if said := shareSaid.String(); !strings.Contains(said, "sender may send") {
		t.Errorf("the dropbox did not say who may reach it:\n%s", said)
	}

	waitFor(t, "the dropbox to be listed", 30*time.Second, func() bool {
		return strings.Contains(sender.must("ls", "taker"), "/share")
	})

	up := filepath.Join(t.TempDir(), "hello.txt")
	writeAt(t, up, "into the dropbox\n")
	sender.must("to", "taker/share", up)

	waitFor(t, "the file to land", 30*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(into, "hello.txt"))
		return err == nil
	})
	if got := read(t, filepath.Join(into, "hello.txt")); got != "into the dropbox\n" {
		t.Errorf("what landed is %q", got)
	}

	// One transfer, and the path goes with it rather than answering with nothing behind it.
	waitFor(t, "the dropbox to close", 30*time.Second, func() bool {
		return strings.Contains(takerSaid.String(), "closed")
	})
	if out := sender.must("ls", "taker"); strings.Contains(out, "/share") {
		t.Errorf("the dropbox outlived the transfer:\n%s", out)
	}
	if !strings.Contains(shareSaid.String(), "a transfer finished") {
		t.Errorf("`drop share` did not say the transfer was over:\n%s", shareSaid.String())
	}
}

// within gives a command with something on standard input the same room `run` gives every other.
func within(t *testing.T) context.Context {
	t.Helper()

	ctx, stop := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(stop)
	return ctx
}

// writeAt puts a file where the test wants one, making whatever it needs on the way.
func writeAt(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(got)
}
