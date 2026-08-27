package note

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/history"
)

// The file and the history, kept level with each other.
//
// A real log on a real disk, because what is being checked is what a scan makes of what is written
// there — and above all that what drop itself wrote is not read back as somebody having edited it,
// which is the failure that shows up as two machines never agreeing.

// asSomebody gives this machine a user key to sign changes with, and says what it is.
func asSomebody(t *testing.T, name string) string {
	t.Helper()

	at := filepath.Join(t.TempDir(), name)
	public, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a user key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(secret, name)
	if err != nil {
		t.Fatalf("writing a user key: %v", err)
	}
	if err := os.WriteFile(at, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROP_USER_KEY", at)

	signer, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return string(ssh.MarshalAuthorizedKey(signer))
}

// aKeeper is one note on a disk of its own.
func aKeeper(t *testing.T) *keeper {
	t.Helper()

	t.Setenv("XDG_DATA_HOME", t.TempDir())
	l, err := history.Open("0123456789abcdef")
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	return &keeper{file: filepath.Join(t.TempDir(), "notes.md"), log: l}
}

func (k *keeper) turn(t *testing.T) bool {
	t.Helper()

	made, err := k.once()
	if err != nil {
		t.Fatalf("once(): %v", err)
	}
	return made
}

func (k *keeper) count(t *testing.T) int {
	t.Helper()

	changes, err := k.log.Ordered()
	if err != nil {
		t.Fatalf("Ordered(): %v", err)
	}
	return len(changes)
}

// save writes a file the way a person's editor leaves one, and then dates it far enough back that
// it counts as put down rather than still being written. A test that saved and read in the same
// breath would be asking about a file nobody has finished with.
func save(t *testing.T, at, body string) {
	t.Helper()

	if err := os.WriteFile(at, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	settled(t, at)
}

// settled backdates a file so steady() takes it for one nobody is holding open.
func settled(t *testing.T, at string) {
	t.Helper()

	when := time.Now().Add(-2 * Still)
	if err := os.Chtimes(at, when, when); err != nil {
		t.Fatal(err)
	}
}

func held(t *testing.T, at string) string {
	t.Helper()

	raw, err := os.ReadFile(at)
	if err != nil {
		t.Fatalf("reading %s: %v", at, err)
	}
	return string(raw)
}

// A change written by drop itself must not come back round as somebody's edit. Two machines that
// got this wrong would hand one change back and forth for as long as they were both running.
func TestAFileDropWroteIsNotReadBackAsAChange(t *testing.T) {
	asSomebody(t, "alice")
	k := aKeeper(t)

	save(t, k.file, "one\ntwo\n")
	if !k.turn(t) {
		t.Fatal("saving a file was not noticed")
	}
	if n := k.count(t); n != 1 {
		t.Fatalf("saving one file made %d changes", n)
	}

	// Somebody else's change arrives, and the file drop writes for it is drop's own doing.
	them := asSomebody(t, "bob")
	theirs, err := history.Sign(k.log.At(), []byte("one\ntwo\nthree\n"), k.log.Heads())
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}
	if _, err := k.log.Add(theirs); err != nil {
		t.Fatalf("Add(): %v", err)
	}
	if theirs.Author != them {
		t.Fatalf("the change is signed by %q, want %q", theirs.Author, them)
	}

	if k.turn(t) {
		t.Fatal("taking somebody else's change counted as making one")
	}
	if got, want := held(t, k.file), "one\ntwo\nthree\n"; got != want {
		t.Fatalf("the file holds %q, want %q", got, want)
	}

	for i := range 5 {
		if k.turn(t) {
			t.Fatalf("round %d made a change out of what drop wrote", i)
		}
		if n := k.count(t); n != 2 {
			t.Fatalf("round %d left %d changes, want 2", i, n)
		}
	}
}

// A note picked up again after drop was not running: what somebody typed in the meantime is a
// change, and what drop left there is not.
func TestAnEditMadeWhileDropWasOffIsNoticedAndOneItMadeIsNot(t *testing.T) {
	asSomebody(t, "alice")
	k := aKeeper(t)

	save(t, k.file, "one\n")
	k.turn(t)

	// The same note, from nothing, the way a restart picks it up.
	again := &keeper{file: k.file, log: k.log}
	if again.turn(t) {
		t.Fatal("a file drop wrote before it stopped was taken for an edit")
	}
	if n := again.count(t); n != 1 {
		t.Fatalf("a restart left %d changes, want 1", n)
	}

	save(t, k.file, "one\ntwo\n")
	third := &keeper{file: k.file, log: k.log}
	if !third.turn(t) {
		t.Fatal("an edit made while drop was off was not noticed")
	}
	if n := third.count(t); n != 2 {
		t.Fatalf("the edit made %d changes in all, want 2", n)
	}
}

// A note that only exists as a history — the machine that joined it — gets the file written for it.
func TestANoteWithNoFileYetIsWrittenFromItsHistory(t *testing.T) {
	asSomebody(t, "alice")
	k := aKeeper(t)

	c, err := history.Sign(k.log.At(), []byte("what alice wrote\n"), nil)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}
	if _, err := k.log.Add(c); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	if k.turn(t) {
		t.Fatal("writing the file out counted as making a change")
	}
	if got, want := held(t, k.file), "what alice wrote\n"; got != want {
		t.Fatalf("the file holds %q, want %q", got, want)
	}
	if k.turn(t) || k.count(t) != 1 {
		t.Fatalf("the file that was written came back as a change: %d changes", k.count(t))
	}
}

// Two people, no file in common, and neither loses what they wrote.
func TestTwoMachinesReplayingOneHistoryWriteTheSameFile(t *testing.T) {
	asSomebody(t, "alice")
	mine := aKeeper(t)

	save(t, mine.file, "one\ntwo\nthree\n")
	mine.turn(t)

	first := mine.log.Heads()
	save(t, mine.file, "ONE\ntwo\nthree\n")
	mine.turn(t)

	// Bob, who had seen the first change and not the second.
	asSomebody(t, "bob")
	theirs, err := history.Sign(mine.log.At(), []byte("one\ntwo\nTHREE\n"), first)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}
	if _, err := mine.log.Add(theirs); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	mine.turn(t)
	if got, want := held(t, mine.file), "ONE\ntwo\nTHREE\n"; got != want {
		t.Fatalf("the file holds %q, want %q", got, want)
	}

	// The same history on another machine, whose file has never been written.
	yours := &keeper{file: filepath.Join(t.TempDir(), "notes.md"), log: mine.log}
	yours.turn(t)
	if got := held(t, yours.file); got != held(t, mine.file) {
		t.Fatalf("one machine holds %q and the other %q", got, held(t, mine.file))
	}
}

// Something that is not text: one version becomes the file and the other is kept beside it, under
// a name saying whose it is.
func TestTheVersionThatIsNotTheFileIsKeptBesideIt(t *testing.T) {
	asSomebody(t, "alice")
	k := aKeeper(t)

	save(t, k.file, "SQLite format 3\x00\x10\x00\x01")
	k.turn(t)
	first := k.log.Heads()

	save(t, k.file, "SQLite format 3\x00\x10\x00\x01alice")
	k.turn(t)

	asSomebody(t, "bob")
	theirs, err := history.Sign(k.log.At(), []byte("SQLite format 3\x00\x10\x00\x01bob"), first)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}
	if _, err := k.log.Add(theirs); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	k.turn(t)

	names, err := filepath.Glob(k.file + ".*")
	if err != nil || len(names) != 1 {
		t.Fatalf("beside %s there is %v (%v)", k.file, names, err)
	}
	found := names[0]

	both := held(t, k.file) + held(t, found)
	for _, want := range []string{"alice", "bob"} {
		if !strings.Contains(both, want) {
			t.Fatalf("%s's version is gone: %q beside %q", want, held(t, k.file), held(t, found))
		}
	}
	if strings.Contains(held(t, k.file), "<<<<<<<") {
		t.Fatalf("a database was merged as lines: %q", held(t, k.file))
	}
}

// A file somebody is still writing is left alone rather than merged half-saved.
func TestAFileBeingWrittenIsSkipped(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "missing.md")

	if raw, there, err := steady(at); there || err != nil || raw != nil {
		t.Fatalf("a file that is not there read as %q, %v, %v", raw, there, err)
	}

	save(t, at, "one\n")
	raw, there, err := steady(at)
	if !there || err != nil || string(raw) != "one\n" {
		t.Fatalf("a file that is there read as %q, %v, %v", raw, there, err)
	}
}
