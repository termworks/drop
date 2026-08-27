package files

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/wire"
)

// What a folder kept level on two machines needs of a directory, and what it did not have.
//
// Each of these is one thing that made the difference between a folder that converges and one that
// hands the same file back and forth: a time that cannot tell two saves apart, a write that cannot
// take the place of a file, an arriving file dated now, a mode that cannot be compared, and a
// transfer that starts again from nothing every time it is interrupted.

// counted is one side of a stream that says how much it has read, and stops reading once it has
// read as much as a test wants it to.
type counted struct {
	net.Conn
	read  int64
	limit int64
}

func (c *counted) Read(p []byte) (int, error) {
	if c.limit > 0 && c.read >= c.limit {
		return 0, errors.New("the connection went away")
	}
	n, err := c.Conn.Read(p)
	c.read += int64(n)
	return n, err
}

// weighed runs a files namespace and walks it over a stream that counts what comes back.
func weighed(t *testing.T, dir string, writable bool, limit int64) (*Browsing, *counted) {
	t.Helper()

	caller, server := net.Pipe()
	t.Cleanup(func() { caller.Close() })

	go func() {
		defer server.Close()

		at := arch.Session{
			Path:   "/files",
			Config: Config{Dir: dir, Writable: writable},
			Conn:   wire.NewConn(server),
		}
		_ = New(Into{}).Serve(t.Context(), at)
	}()

	side := &counted{Conn: caller, limit: limit}
	b, err := Browse(wire.NewConn(side))
	if err != nil {
		t.Fatalf("Browse(): %v", err)
	}
	return b, side
}

// dating puts an exact modification time on a file.
func dating(t *testing.T, at string, when time.Time) {
	t.Helper()

	if err := os.Chtimes(at, when, when); err != nil {
		t.Fatalf("dating %s: %v", at, err)
	}
}

// changed is what a listing says about one name.
func changed(t *testing.T, b *Browsing, name string) Entry {
	t.Helper()

	entries, err := b.List("")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("%s is not in the listing", name)
	return Entry{}
}

// A save and the save three milliseconds after it are two saves. At whole seconds they are one, and
// a folder held up against the times in a listing would never see the second.
func TestTwoSavesAMillisecondApartAreTwoSaves(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(at, []byte("one"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	b, _ := weighed(t, dir, false, 0)

	first := time.Date(2026, 3, 4, 5, 6, 7, 111_000_000, time.UTC)
	dating(t, at, first)
	one := changed(t, b, "notes.txt")

	dating(t, at, first.Add(3*time.Millisecond))
	two := changed(t, b, "notes.txt")

	if one.At != first.UnixNano() {
		t.Errorf("the listing dates it %d, and the filesystem says %d", one.At, first.UnixNano())
	}
	if one.At == two.At {
		t.Fatalf("two saves 3ms apart are both dated %d", one.At)
	}
	if got := two.At - one.At; got != int64(3*time.Millisecond) {
		t.Errorf("the two saves are %dns apart, want %d", got, 3*time.Millisecond)
	}
}

// A put hands a file over and never loses what is already here, so it lands beside it. A replace is
// one version of a file taking the place of another, and it says which version it believes it is
// replacing.
func TestAReplaceTakesThePlaceOfAFileAndAPutDoesNot(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(at, []byte("first"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	was := blake3.Sum256([]byte("first"))

	b := opened(t, dir, true, Into{})

	if err := b.Put("notes.txt", strings.NewReader("handed over"), Given{Size: 11, Mode: 0o644}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if got := read(t, at); string(got) != "first" {
		t.Errorf("a put wrote over the file, which now says %q", got)
	}
	if got := read(t, filepath.Join(dir, "notes-1.txt")); string(got) != "handed over" {
		t.Errorf("the put landed as %q", got)
	}

	if err := b.Replace("notes.txt", strings.NewReader("second"), was[:], Given{Size: 6, Mode: 0o644}); err != nil {
		t.Fatalf("Replace(): %v", err)
	}
	if got := read(t, at); string(got) != "second" {
		t.Fatalf("a replace left the file saying %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes-2.txt")); err == nil {
		t.Error("a replace numbered its way around the file it was replacing")
	}
}

// The precondition is the whole point: a version that somebody else has since changed is refused
// rather than written over, and the file they wrote is still there afterwards.
func TestAReplaceRefusesAVersionSomebodyElseChanged(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(at, []byte("theirs"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	stale := blake3.Sum256([]byte("what I last read"))
	b := opened(t, dir, true, Into{})

	err := b.Replace("notes.txt", strings.NewReader("mine"), stale[:], Given{Size: 4, Mode: 0o644})
	if err == nil {
		t.Fatal("a replace against a version that is not there succeeded")
	}
	if !strings.Contains(err.Error(), "changed since you last read it") {
		t.Errorf("it was refused as %v", err)
	}
	if got := read(t, at); string(got) != "theirs" {
		t.Errorf("the file was written over anyway, and now says %q", got)
	}

	// And the other way round: a caller who believes there is nothing there, when there is.
	if err := b.Replace("notes.txt", strings.NewReader("mine"), nil, Given{Size: 4}); err == nil {
		t.Error("a replace onto a name that is taken was allowed as a first write")
	}
	if _, err := b.List(""); err != nil {
		t.Fatalf("the session did not survive two refusals: %v", err)
	}
}

// A file that arrives keeps the time it had where it came from. Dated now, it reads on the next
// scan as a file somebody has just edited here, and goes straight back where it came from.
func TestAnArrivingFileKeepsTheTimeItHad(t *testing.T) {
	there, here := t.TempDir(), t.TempDir()
	at := filepath.Join(there, "notes.txt")
	if err := os.WriteFile(at, []byte("what happened"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	when := time.Date(2024, 1, 2, 3, 4, 5, 123_456_789, time.UTC)
	dating(t, at, when)

	b := opened(t, there, false, Into{})
	into := filepath.Join(here, "copy.txt")
	if err := b.Get("notes.txt", into, Want{}); err != nil {
		t.Fatalf("Get(): %v", err)
	}

	stat, err := os.Stat(into)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := stat.ModTime().UnixNano(); got != when.UnixNano() {
		t.Errorf("it landed dated %s, want %s", stat.ModTime().UTC(), when)
	}

	// The same in the other direction: a file pushed into a namespace is dated where it came from.
	taking := opened(t, here, true, Into{})
	if err := taking.PutFile("pushed.txt", at, nil); err != nil {
		t.Fatalf("PutFile(): %v", err)
	}
	landed, err := os.Stat(filepath.Join(here, "pushed.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := landed.ModTime().UnixNano(); got != when.UnixNano() {
		t.Errorf("the push landed dated %s, want %s", landed.ModTime().UTC(), when)
	}
}

// The mode a file arrives with is flattened, so two files that were 0755 and 0750 where they came
// from are one mode here. That is deliberate, and it is why mode is not something two machines hold
// a file up against: they would find it different every time and write it back at each other.
func TestTheModeAFileArrivesWithIsNotTheModeItHad(t *testing.T) {
	there, here := t.TempDir(), t.TempDir()
	for name, mode := range map[string]os.FileMode{"open": 0o755, "closer": 0o750, "plain": 0o644} {
		if err := os.WriteFile(filepath.Join(there, name), []byte(name), mode); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		if err := os.Chmod(filepath.Join(there, name), mode); err != nil {
			t.Fatalf("setting the mode of %s: %v", name, err)
		}
	}

	b := opened(t, there, false, Into{})
	for _, name := range []string{"open", "closer", "plain"} {
		if err := b.Get(name, filepath.Join(here, name), Want{}); err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
	}

	modeOf := func(where, name string) os.FileMode {
		stat, err := os.Stat(filepath.Join(where, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		return stat.Mode().Perm()
	}

	if a, b := modeOf(there, "open"), modeOf(there, "closer"); a == b {
		t.Fatalf("0755 and 0750 are already the same mode where they came from: %o", a)
	}
	if a, b := modeOf(here, "open"), modeOf(here, "closer"); a != b {
		t.Errorf("two modes that arrived survive as %o and %o", a, b)
	}
	if got := modeOf(here, "open"); got != 0o700 {
		t.Errorf("a program arrived as %o, want 700", got)
	}
	if got := modeOf(here, "plain"); got != 0o600 {
		t.Errorf("a file arrived as %o, want 600", got)
	}
}

// A get that is killed halfway leaves what it had where the next attempt for the same bytes finds
// it, and the next attempt sends only what is missing.
func TestAGetCarriesOnFromWhereItWasKilled(t *testing.T) {
	there, here := t.TempDir(), t.TempDir()
	body := bytes.Repeat([]byte("the numbers, over and over. "), 40_000)
	if err := os.WriteFile(filepath.Join(there, "report.bin"), body, 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	sum := blake3.Sum256(body)

	into := filepath.Join(here, "report.bin")
	want := Want{Sum: sum[:]}

	// Killed a third of the way in.
	killed, _ := weighed(t, there, false, int64(len(body))/3)
	if err := killed.Get("report.bin", into, want); err == nil {
		t.Fatal("a get whose connection went away succeeded")
	}

	part := filepath.Join(here, partFor("report.bin", sum[:]))
	stat, err := os.Stat(part)
	if err != nil {
		t.Fatalf("the part a killed get was filling is gone: %v", err)
	}
	if stat.Size() == 0 {
		t.Fatal("the part a killed get was filling is empty")
	}

	again, side := weighed(t, there, false, 0)
	if err := again.Get("report.bin", into, want); err != nil {
		t.Fatalf("the second Get(): %v", err)
	}
	if got := read(t, into); !bytes.Equal(got, body) {
		t.Fatalf("the file came out as %d bytes, want %d", len(got), len(body))
	}
	if side.read >= int64(len(body)) {
		t.Errorf("the second get moved %d bytes of a %d byte file it already half held", side.read, len(body))
	}
	if _, err := os.Stat(part); err == nil {
		t.Error("the part is still there after the file landed")
	}
}

// And a get that has no digest to name a part after cannot be carried on, so nothing is left lying
// about under a name nothing would ever recognise.
func TestAGetWithNoDigestLeavesNothingBehind(t *testing.T) {
	there, here := t.TempDir(), t.TempDir()
	body := bytes.Repeat([]byte("x"), 400_000)
	if err := os.WriteFile(filepath.Join(there, "report.bin"), body, 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	killed, _ := weighed(t, there, false, int64(len(body))/3)
	if err := killed.Get("report.bin", filepath.Join(here, "report.bin"), Want{}); err == nil {
		t.Fatal("a get whose connection went away succeeded")
	}

	left, err := os.ReadDir(here)
	if err != nil {
		t.Fatalf("reading the directory: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("a get that cannot be carried on left %d files behind", len(left))
	}
}

// What was announced is weighed against what arrived. Without that, a transfer that ends early and
// counts what it sent honestly is taken as a whole file.
func TestWhatArrivesIsWeighedAgainstWhatWasAnnounced(t *testing.T) {
	dir := t.TempDir()
	b := opened(t, dir, true, Into{})

	err := b.Put("report.bin", strings.NewReader("short"), Given{Size: 8 << 20, Mode: 0o644})
	if err == nil {
		t.Fatal("a put that announced 8 MiB and sent five bytes was taken")
	}
	if !strings.Contains(err.Error(), "were announced") {
		t.Errorf("it was refused as %v", err)
	}

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the directory: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("a put that did not weigh out left %d files behind", len(left))
	}
}

// A directory too big to answer says what to do about it rather than only that it will not.
func TestADirectoryTooBigToListSaysWhatToDo(t *testing.T) {
	dir := t.TempDir()
	for i := range MaxEntries + 1 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%05d", i)), nil, 0o600); err != nil {
			t.Fatalf("writing entry %d: %v", i, err)
		}
	}

	b := opened(t, dir, false, Into{})
	_, err := b.List("")
	if err == nil {
		t.Fatal("a directory over the limit was listed")
	}
	for _, want := range []string{"ask for a directory inside it by name", "namespace of its own"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}
