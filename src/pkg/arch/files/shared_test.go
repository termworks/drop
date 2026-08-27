package files

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/history"
)

// A folder several people change at once.
//
// Two real directories, two real histories on two real disks, and no moment at which either machine
// can see the other. What is being checked is the thing somebody asked for — that two people
// working in one folder both keep what they did — and the thing that makes it work at all, that
// every machine arrives at the same folder.

// person is one machine's copy of a shared folder: its own directory, its own history, its own key,
// and a count of the bytes it has had to fetch from anybody else.
type person struct {
	name  string
	dir   string
	data  string
	key   string
	log   *history.Log
	k     *keeper
	moved int64
}

// joins makes one machine holding a folder that every machine calls by the same name.
func joins(t *testing.T, name string) *person {
	t.Helper()

	p := &person{name: name, dir: t.TempDir(), data: t.TempDir(), key: signing(t, name)}
	p.at(t)

	l, err := history.Open("0123456789abcdef")
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	p.log = l
	p.k = &keeper{path: "/work", dir: p.dir, log: l}
	return p
}

// signing gives a machine a user key of its own and says where it is.
func signing(t *testing.T, name string) string {
	t.Helper()

	at := filepath.Join(t.TempDir(), name)
	_, secret, err := ed25519.GenerateKey(rand.Reader)
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
	return at
}

// at makes this the machine whose disk and whose key are in use.
func (p *person) at(t *testing.T) {
	t.Helper()

	t.Setenv("XDG_DATA_HOME", p.data)
	t.Setenv("DROP_USER_KEY", p.key)
}

// turn is one round of this machine's timer, with somebody to fetch from or nobody at all.
func (p *person) turn(t *testing.T, from *person) error {
	t.Helper()

	p.at(t)
	_, err := p.k.once(p.fetching(from))
	return err
}

func (p *person) must(t *testing.T, from *person) {
	t.Helper()

	if err := p.turn(t, from); err != nil {
		t.Fatalf("%s: %v", p.name, err)
	}
}

// fetching is what this machine does for a file whose bytes are not in a change: it reads them off
// the other machine's disk, which is what a get over a files session comes to.
func (p *person) fetching(from *person) func(Wanted) error {
	return func(w Wanted) error {
		if from == nil {
			return fmt.Errorf("nobody here holds %s", w.Name)
		}
		raw, err := os.ReadFile(filepath.Join(from.dir, filepath.FromSlash(w.Name)))
		if err != nil {
			return fmt.Errorf("%s does not have %s", from.name, w.Name)
		}
		p.moved += int64(len(raw))
		return os.WriteFile(w.Into, raw, 0o600)
	}
}

// folder is what this machine's history says the folder holds.
func (p *person) folder(t *testing.T) Folder {
	t.Helper()

	p.at(t)
	want, err := p.k.folder()
	if err != nil {
		t.Fatalf("%s: %v", p.name, err)
	}
	return want
}

// meets carries the changes each machine has that the other has not, which is what a meeting does
// over a wire and this does across a table.
func meets(t *testing.T, a, b *person) {
	t.Helper()

	carry(t, a, b)
	carry(t, b, a)
}

func carry(t *testing.T, from, to *person) {
	t.Helper()

	owed, err := from.log.Since(to.log.Heads())
	if err != nil {
		t.Fatalf("Since(): %v", err)
	}
	for _, c := range owed {
		if _, err := to.log.Add(c); err != nil {
			t.Fatalf("Add(): %v", err)
		}
	}
}

// together runs both machines against each other until nothing more moves, the way two timers on
// two machines do. A round that cannot finish is not a failure: the file it was waiting for is very
// often one the other machine has not written yet.
func together(t *testing.T, a, b *person) {
	t.Helper()

	for range 4 {
		meets(t, a, b)
		_ = a.turn(t, b)
		_ = b.turn(t, a)
	}
	meets(t, a, b)
	a.must(t, b)
	b.must(t, a)
}

// save writes a file into a folder the way a person's editor does.
func save(t *testing.T, dir, name, body string) {
	t.Helper()

	at := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(at, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// holding is what a folder holds at one path, and nothing when it holds nothing there.
func holding(t *testing.T, p *person, name string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(p.dir, filepath.FromSlash(name)))
	if err != nil {
		return ""
	}
	return string(raw)
}

// listing is every file in a folder, by path, with what it holds.
func listing(t *testing.T, p *person) map[string]string {
	t.Helper()

	out := map[string]string{}
	err := filepath.WalkDir(p.dir, func(at string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(p.dir, at)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(at)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", p.dir, err)
	}
	return out
}

// alike says the two machines hold the same folder, and what differs when they do not.
func alike(t *testing.T, a, b *person) {
	t.Helper()

	mine, yours := listing(t, a), listing(t, b)
	for _, path := range union(mine, yours) {
		if mine[path] != yours[path] {
			t.Fatalf("%s holds %q at %s and %s holds %q", a.name, mine[path], path, b.name, yours[path])
		}
	}
}

func union(a, b map[string]string) []string {
	seen := map[string]bool{}
	for path := range a {
		seen[path] = true
	}
	for path := range b {
		seen[path] = true
	}

	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func blake3Of(body string) [32]byte { return blake3.Sum256([]byte(body)) }

// tidied is a set of heads the way a change writes them.
func tidied(heads []history.ID) []history.ID {
	out := append([]history.ID(nil), heads...)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out
}

func TestAFileAddedOnEachSideAppearsOnBoth(t *testing.T) {
	alice, bob := joins(t, "alice"), joins(t, "bob")

	save(t, alice.dir, "notes/hers.txt", "what alice wrote\n")
	save(t, bob.dir, "notes/his.txt", "what bob wrote\n")
	together(t, alice, bob)

	for _, p := range []*person{alice, bob} {
		if got := holding(t, p, "notes/hers.txt"); got != "what alice wrote\n" {
			t.Errorf("%s holds %q at notes/hers.txt", p.name, got)
		}
		if got := holding(t, p, "notes/his.txt"); got != "what bob wrote\n" {
			t.Errorf("%s holds %q at notes/his.txt", p.name, got)
		}
	}
	alike(t, alice, bob)
}

func TestTwoPeopleEditOneFileInDifferentPlacesAndBothEditsSurvive(t *testing.T) {
	alice, bob := joins(t, "alice"), joins(t, "bob")

	save(t, alice.dir, "notes.txt", "one\ntwo\nthree\nfour\nfive\n")
	together(t, alice, bob)

	// And now neither can see the other.
	save(t, alice.dir, "notes.txt", "ONE\ntwo\nthree\nfour\nfive\n")
	save(t, bob.dir, "notes.txt", "one\ntwo\nthree\nfour\nFIVE\n")
	alice.must(t, nil)
	bob.must(t, nil)

	together(t, alice, bob)

	const both = "ONE\ntwo\nthree\nfour\nFIVE\n"
	for _, p := range []*person{alice, bob} {
		if got := holding(t, p, "notes.txt"); got != both {
			t.Errorf("%s holds\n%s\nwant\n%s", p.name, got, both)
		}
	}
	alike(t, alice, bob)
}

func TestTwoPeopleEditOneLineAndNeitherEditIsLost(t *testing.T) {
	alice, bob := joins(t, "alice"), joins(t, "bob")

	save(t, alice.dir, "notes.txt", "one\ntwo\nthree\n")
	together(t, alice, bob)

	save(t, alice.dir, "notes.txt", "one\nby alice\nthree\n")
	save(t, bob.dir, "notes.txt", "one\nby bob\nthree\n")
	alice.must(t, nil)
	bob.must(t, nil)

	together(t, alice, bob)
	alike(t, alice, bob)

	text := holding(t, alice, "notes.txt")
	t.Logf("the conflicted file:\n%s", text)
	for _, want := range []string{"<<<<<<< ", "=======\n", ">>>>>>> ", "by alice\n", "by bob\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the merge does not hold %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(text, "one\n") || !strings.HasSuffix(text, "three\n") {
		t.Fatalf("what nobody touched did not survive:\n%s", text)
	}
}

// A deletion is a change like any other, and it travels. A file that never arrived is not a
// deletion, and saying it was would delete it everywhere it does exist.
func TestADeletionTravelsAndAFileThatNeverArrivedDoesNot(t *testing.T) {
	alice, bob := joins(t, "alice"), joins(t, "bob")

	away := bytes.Repeat([]byte{0x00, 0xff, 0x7f}, 60_000)
	save(t, alice.dir, "old.txt", "to be removed\n")
	if err := os.WriteFile(filepath.Join(alice.dir, "big.bin"), away, 0o600); err != nil {
		t.Fatal(err)
	}
	alice.must(t, nil)

	// Bob takes the changes but has nobody to fetch the bytes of the big one from.
	meets(t, alice, bob)
	if err := bob.turn(t, nil); err == nil {
		t.Fatal("bob was handed a file nobody could give him")
	}
	if got := holding(t, bob, "old.txt"); got != "to be removed\n" {
		t.Fatalf("the small file did not arrive: %q", got)
	}
	if _, err := os.Stat(filepath.Join(bob.dir, "big.bin")); err == nil {
		t.Fatal("the big file arrived with nobody to fetch it from")
	}

	// Alice removes the file bob does have.
	if err := os.Remove(filepath.Join(alice.dir, "old.txt")); err != nil {
		t.Fatal(err)
	}
	alice.must(t, nil)

	meets(t, alice, bob)
	_ = bob.turn(t, nil)

	if _, err := os.Stat(filepath.Join(bob.dir, "old.txt")); err == nil {
		t.Error("a file deleted where it lived is still here")
	}

	// And bob never said the file he has not got was deleted, so alice still has it.
	meets(t, alice, bob)
	alice.must(t, nil)
	if got := alice.folder(t)["big.bin"]; got.Gone {
		t.Fatal("a file that never arrived was taken for one somebody deleted")
	}
	if _, err := os.Stat(filepath.Join(alice.dir, "big.bin")); err != nil {
		t.Fatalf("a file that never arrived was deleted where it does exist: %v", err)
	}
}

// A rename is a delete and a create, and the bytes are already here under the old name.
func TestARenameMovesNoBytes(t *testing.T) {
	alice, bob := joins(t, "alice"), joins(t, "bob")

	body := bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47, 0x00}, 40_000)
	if err := os.WriteFile(filepath.Join(alice.dir, "report.bin"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	together(t, alice, bob)

	if bob.moved != int64(len(body)) {
		t.Fatalf("the first copy moved %d bytes of a %d byte file", bob.moved, len(body))
	}
	was := bob.moved

	if err := os.MkdirAll(filepath.Join(alice.dir, "archive"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(alice.dir, "report.bin"), filepath.Join(alice.dir, "archive", "report.bin")); err != nil {
		t.Fatal(err)
	}
	together(t, alice, bob)

	if bob.moved != was {
		t.Errorf("a rename moved %d bytes", bob.moved-was)
	}
	if got := []byte(holding(t, bob, "archive/report.bin")); !bytes.Equal(got, body) {
		t.Errorf("the renamed file is %d bytes, want %d", len(got), len(body))
	}
	if _, err := os.Stat(filepath.Join(bob.dir, "report.bin")); err == nil {
		t.Error("the file is still under its old name as well")
	}
	alike(t, alice, bob)
}

// A database is never merged line by line. Both versions are kept, one beside the other, because
// merging one destroys it and there is no undoing that.
func TestSomethingThatIsNotTextIsKeptBothWaysAndNeverMerged(t *testing.T) {
	alice, bob := joins(t, "alice"), joins(t, "bob")

	base := "SQLite format 3\x00\x10\x00\x01\x01\x00@  \x00\x00\x00\x01"
	save(t, alice.dir, "notes.db", base)
	together(t, alice, bob)

	save(t, alice.dir, "notes.db", base+"\x01alice\x00")
	save(t, bob.dir, "notes.db", base+"\x02bob\x00")
	alice.must(t, nil)
	bob.must(t, nil)

	together(t, alice, bob)
	alike(t, alice, bob)

	held := listing(t, alice)
	if len(held) != 2 {
		t.Fatalf("two versions came out as %d files: %v", len(held), named(held))
	}
	for path, body := range held {
		if strings.Contains(body, "<<<<<<<") {
			t.Fatalf("a database was merged as lines at %s:\n%q", path, body)
		}
	}
	for _, want := range []string{"alice", "bob"} {
		found := false
		for _, body := range held {
			if strings.Contains(body, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s's version is gone: %v", want, named(held))
		}
	}
}

// Every machine derives the folder from the changes, so the order they arrived in must not show.
func TestTheSameChangesInAnyOrderMakeTheSameFolder(t *testing.T) {
	edit := func(author string, at string, body string, heads ...history.ID) history.Change {
		held := Held{Size: int64(len(body)), Sum: blake3Of(body), At: 1, Body: []byte(body)}
		return history.Change{
			Heads:  tidied(heads),
			Author: author,
			Body:   encodeEdits([]Edit{{Path: at, Held: held}}),
		}
	}
	gone := func(author string, at string, heads ...history.ID) history.Change {
		return history.Change{
			Heads:  tidied(heads),
			Author: author,
			Body:   encodeEdits([]Edit{{Path: at, Held: Held{Gone: true, At: 2}}}),
		}
	}

	first := edit("alice-key", "notes.txt", "one\ntwo\nthree\n")
	hers := edit("alice-key", "notes.txt", "ONE\ntwo\nthree\n", first.ID())
	his := edit("bob-key", "notes.txt", "one\ntwo\nTHREE\n", first.ID())
	mine := edit("alice-key", "hers.txt", "hers\n", first.ID())
	away := gone("bob-key", "hers.txt", mine.ID())

	orders := [][]history.Change{
		{first, hers, his, mine, away},
		{first, mine, away, his, hers},
		{first, his, mine, hers, away},
		{first, mine, hers, away, his},
	}

	var was Folder
	for i, order := range orders {
		made := Changed(order)
		if i == 0 {
			was = made
			continue
		}
		for _, path := range Paths(was) {
			if !was[path].same(made[path]) {
				t.Fatalf("order %d holds %+v at %s, and order 0 holds %+v", i, made[path], path, was[path])
			}
		}
		if len(made) != len(was) {
			t.Fatalf("order %d holds %d paths and order 0 holds %d", i, len(made), len(was))
		}
	}

	if !was["hers.txt"].Gone {
		t.Error("a file somebody deleted came back")
	}
	text := string(was["notes.txt"].Body)
	if !strings.Contains(text, "ONE") || !strings.Contains(text, "THREE") {
		t.Fatalf("somebody's edit is missing:\n%s", text)
	}
}
