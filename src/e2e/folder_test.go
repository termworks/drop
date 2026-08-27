//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// One folder, two people, and no moment at which they can see each other change it.
//
// A note is one file and a change carries the whole of it. A folder cannot work that way, so a
// change here says which path moved and what it now is, and the bytes of anything that will not fit
// in a change travel the way bytes already travel: a get on the machine that has them. What this
// checks is that the two directories come out the same, with everything both people did in them.
func TestAFolderTwoPeopleWorkInAtOnce(t *testing.T) {
	alice := newNode(t, "alice", "47891")
	bob := newNode(t, "bob", "47892")

	hers := filepath.Join(alice.home, "work")
	his := filepath.Join(bob.home, "work")
	for _, dir := range []string{hers, his} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	alice.serves(fmt.Sprintf(`
local drop = require("drop")

drop.mount("/chat", { type = "chat",  access = "paired" })
drop.mount("/work", { type = "files", access = "paired", shared = true, dir = %q, writable = true })
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

	// One file of lines, which travels inside a change, and one that does not, which has to be
	// fetched off the machine that has it.
	picture := bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}, 20_000)

	t.Run("what alice puts in her folder is the folder", func(t *testing.T) {
		writing(t, filepath.Join(hers, "notes.md"), "one\ntwo\nthree\nfour\nfive\n")
		if err := os.WriteFile(filepath.Join(hers, "picture.png"), picture, 0o644); err != nil {
			t.Fatal(err)
		}

		out := alice.must("path", "ls")
		if !strings.Contains(out, "/work") || !strings.Contains(out, "files") {
			t.Fatalf("/work is not served as files:\n%s", out)
		}
	})

	t.Run("bob joins it and both files reach his disk", func(t *testing.T) {
		out := bob.must("path", "join", "alice:/work", "--set", "dir="+his, "--flag", "writable")
		if !strings.Contains(out, "files, shared") {
			t.Fatalf("joining did not say what it is:\n%s", out)
		}

		waitFor(t, "the folder to reach bob", 90*time.Second, func() bool {
			return reading(t, filepath.Join(his, "notes.md")) == "one\ntwo\nthree\nfour\nfive\n" &&
				bytes.Equal(held(t, filepath.Join(his, "picture.png")), picture)
		})
	})

	t.Run("each works in it while neither can reach the other", func(t *testing.T) {
		stopAlice()
		stopBob()

		writing(t, filepath.Join(hers, "notes.md"), "ONE\ntwo\nthree\nfour\nfive\n")
		writing(t, filepath.Join(hers, "hers.txt"), "what alice wrote\n")
		writing(t, filepath.Join(his, "notes.md"), "one\ntwo\nthree\nfour\nFIVE\n")
		writing(t, filepath.Join(his, "his.txt"), "what bob wrote\n")
	})

	t.Run("and when they meet again both folders are the same folder", func(t *testing.T) {
		_, aliceAgain, stopAlice := alice.background("serve")
		defer stopAlice()
		_, bobAgain, stopBob := bob.background("serve")
		defer stopBob()

		waitFor(t, "both nodes to be ready again", 30*time.Second, func() bool {
			return strings.Contains(aliceAgain.String(), "ready") && strings.Contains(bobAgain.String(), "ready")
		})

		const both = "ONE\ntwo\nthree\nfour\nFIVE\n"
		waitFor(t, "the two folders to come out the same", 180*time.Second, func() bool {
			return same(walked(t, hers), walked(t, his)) &&
				reading(t, filepath.Join(hers, "notes.md")) == both
		})

		mine, yours := walked(t, hers), walked(t, his)
		if !same(mine, yours) {
			t.Fatalf("alice holds %v and bob holds %v", names(mine), names(yours))
		}
		for _, want := range []string{"notes.md", "hers.txt", "his.txt", "picture.png"} {
			if _, held := mine[want]; !held {
				t.Errorf("%s is in neither folder: %v", want, names(mine))
			}
		}
		if got := mine["notes.md"]; string(got) != both {
			t.Errorf("the file both of them edited says\n%s\nwant\n%s", got, both)
		}
		if strings.Contains(string(mine["notes.md"]), "<<<<<<<") {
			t.Errorf("nobody touched the same line and it was marked anyway:\n%s", mine["notes.md"])
		}
		if !bytes.Equal(mine["picture.png"], picture) {
			t.Errorf("the file that is not text is %d bytes, want %d", len(mine["picture.png"]), len(picture))
		}
	})
}

// held is what a file holds, and nothing when it is not there yet.
func held(t *testing.T, at string) []byte {
	t.Helper()

	raw, err := os.ReadFile(at)
	if err != nil {
		return nil
	}
	return raw
}

// walked is every file in a directory, by path, with what it holds. Part files are left out: they
// are transfers in flight rather than files.
func walked(t *testing.T, dir string) map[string][]byte {
	t.Helper()

	out := map[string][]byte{}
	_ = filepath.WalkDir(dir, func(at string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, at)
		if err != nil {
			return nil
		}
		name := filepath.ToSlash(rel)
		if base := filepath.Base(name); strings.HasPrefix(base, ".") && strings.HasSuffix(base, ".part") {
			return nil
		}
		raw, err := os.ReadFile(at)
		if err != nil {
			return nil
		}
		out[name] = raw
		return nil
	})
	return out
}

// same reports whether two folders hold the same files with the same bytes in them.
func same(mine, yours map[string][]byte) bool {
	if len(mine) != len(yours) {
		return false
	}
	for path, body := range mine {
		if !bytes.Equal(body, yours[path]) {
			return false
		}
	}
	return true
}

// names is what a folder holds, for a message somebody has to read.
func names(of map[string][]byte) []string {
	out := make([]string, 0, len(of))
	for path := range of {
		out = append(out, path)
	}
	return out
}
