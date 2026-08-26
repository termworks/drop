package history

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// asSomebody gives this machine a user key to sign changes with.
func asSomebody(t *testing.T) {
	t.Helper()

	at := filepath.Join(t.TempDir(), "user")
	_, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a user key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(secret, "a test")
	if err != nil {
		t.Fatalf("writing a user key: %v", err)
	}
	if err := os.WriteFile(at, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROP_USER_KEY", at)
}

// aLog is one thing's record, in a data directory of its own.
func aLog(t *testing.T, at string) *Log {
	t.Helper()

	t.Setenv("XDG_DATA_HOME", t.TempDir())
	l, err := Open(at)
	if err != nil {
		t.Fatalf("Open(%q): %v", at, err)
	}
	return l
}

func signed(t *testing.T, body string, heads ...ID) Change {
	t.Helper()

	c, err := Sign([]byte(body), heads)
	if err != nil {
		t.Fatalf("Sign(%q): %v", body, err)
	}
	return c
}

func add(t *testing.T, l *Log, changes ...Change) {
	t.Helper()

	for _, c := range changes {
		if _, err := l.Add(c); err != nil {
			t.Fatalf("Add(%q): %v", c.Body, err)
		}
	}
}

func bodies(changes []Change) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, string(c.Body))
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func smaller(a, b Change) Change {
	if a.ID().String() < b.ID().String() {
		return a
	}
	return b
}

// The whole reason the package exists: two machines given the same changes in different orders
// read the same history, including across a fork nobody resolved.
func TestTwoLogsGivenTheSameChangesInDifferentOrdersReadTheSame(t *testing.T) {
	asSomebody(t)

	first := signed(t, "first")
	left := signed(t, "left", first.ID())
	right := signed(t, "right", first.ID())
	join := signed(t, "join", left.ID(), right.ID())

	mine := aLog(t, "thing")
	theirs := aLog(t, "thing")

	add(t, mine, first, left, right, join)
	add(t, theirs, first, right, left, join)

	ours, err := mine.Ordered()
	if err != nil {
		t.Fatalf("Ordered(): %v", err)
	}
	yours, err := theirs.Ordered()
	if err != nil {
		t.Fatalf("Ordered(): %v", err)
	}

	if !same(bodies(ours), bodies(yours)) {
		t.Fatalf("two logs read differently: %v and %v", bodies(ours), bodies(yours))
	}

	// The tie between the two sides of the fork is broken on id, so one of the two logs read them
	// back the other way round from the way it took them.
	want := []string{"first", string(smaller(left, right).Body), "", "join"}
	if string(smaller(left, right).Body) == "left" {
		want[2] = "right"
	} else {
		want[2] = "left"
	}
	if !same(bodies(ours), want) {
		t.Fatalf("Ordered() = %v, want %v", bodies(ours), want)
	}
}

// The signature is over the change, not beside it.
func TestAChangeWhoseBodyIsAlteredDoesNotVerify(t *testing.T) {
	asSomebody(t)

	c := signed(t, "the body as written")
	c.Body[3] ^= 1

	l := aLog(t, "thing")
	if _, err := l.Add(c); err == nil {
		t.Fatal("an altered change was taken")
	} else if !strings.Contains(err.Error(), "who made it") {
		t.Fatalf("Add() = %v", err)
	}
}

// A change is named by its own bytes, so a record filed under any other name is not that change.
func TestAChangeWhoseIDDoesNotMatchItsBytesIsRefused(t *testing.T) {
	asSomebody(t)

	raw := record(signed(t, "the body as written"))
	if _, err := unrecord(raw); err != nil {
		t.Fatalf("unrecord(): %v", err)
	}

	// Three bytes in: past the length of the id field, and into the id itself.
	raw[3] ^= 1
	if _, err := unrecord(raw); err == nil {
		t.Fatal("a record under the wrong name was read")
	} else if !strings.Contains(err.Error(), "filed as") {
		t.Fatalf("unrecord() = %v", err)
	}
}

// A change naming something nobody here has seen cannot be placed in any order, so taking it would
// be taking something that can never be replayed.
func TestAChangeNamingSomethingUnseenIsRefusedUntilItArrives(t *testing.T) {
	asSomebody(t)

	first := signed(t, "first")
	second := signed(t, "second", first.ID())

	l := aLog(t, "thing")
	if _, err := l.Add(second); err == nil {
		t.Fatal("an orphan was taken")
	} else if !strings.Contains(err.Error(), "not here") {
		t.Fatalf("Add() = %v", err)
	}

	add(t, l, first, second)

	order, err := l.Ordered()
	if err != nil {
		t.Fatalf("Ordered(): %v", err)
	}
	if !same(bodies(order), []string{"first", "second"}) {
		t.Fatalf("Ordered() = %v", bodies(order))
	}
}

// Delivering a change twice is harmless: the second one writes nothing and answers the same name.
func TestAddingTheSameChangeTwiceChangesNothing(t *testing.T) {
	asSomebody(t)

	l := aLog(t, "thing")
	c := signed(t, "said once")

	first, err := l.Add(c)
	if err != nil {
		t.Fatalf("Add(): %v", err)
	}
	written, err := os.Stat(l.file)
	if err != nil {
		t.Fatal(err)
	}

	again, err := l.Add(c)
	if err != nil {
		t.Fatalf("Add() again: %v", err)
	}
	if again != first {
		t.Fatalf("the same change was named %s and then %s", first, again)
	}

	now, err := os.Stat(l.file)
	if err != nil {
		t.Fatal(err)
	}
	if now.Size() != written.Size() {
		t.Fatalf("the log grew from %d to %d bytes", written.Size(), now.Size())
	}

	order, err := l.Ordered()
	if err != nil {
		t.Fatalf("Ordered(): %v", err)
	}
	if len(order) != 1 {
		t.Fatalf("Ordered() = %v", bodies(order))
	}
}

// A fork is two heads until somebody joins it, and one afterwards.
func TestHeadsAreBothSidesOfAForkAndOneAfterAJoin(t *testing.T) {
	asSomebody(t)

	first := signed(t, "first")
	left := signed(t, "left", first.ID())
	right := signed(t, "right", first.ID())

	l := aLog(t, "thing")
	add(t, l, first, left, right)

	heads := l.Heads()
	if len(heads) != 2 {
		t.Fatalf("Heads() = %v, want both sides of the fork", heads)
	}
	want := tidy([]ID{left.ID(), right.ID()})
	if heads[0] != want[0] || heads[1] != want[1] {
		t.Fatalf("Heads() = %v, want %v", heads, want)
	}

	join := signed(t, "join", heads...)
	add(t, l, join)

	heads = l.Heads()
	if len(heads) != 1 || heads[0] != join.ID() {
		t.Fatalf("Heads() = %v, want just the join", heads)
	}
}

// What to send somebody is what they are behind on, and nothing when they are not behind.
func TestSinceIsWhatThePeerHasNotSeen(t *testing.T) {
	asSomebody(t)

	first := signed(t, "first")
	left := signed(t, "left", first.ID())
	right := signed(t, "right", first.ID())
	join := signed(t, "join", left.ID(), right.ID())

	l := aLog(t, "thing")
	add(t, l, first, left, right, join)

	behind, err := l.Since([]ID{first.ID()})
	if err != nil {
		t.Fatalf("Since(): %v", err)
	}
	if len(behind) != 3 {
		t.Fatalf("Since(first) = %v, want the three after it", bodies(behind))
	}
	if string(behind[len(behind)-1].Body) != "join" {
		t.Fatalf("Since() = %v, want the join last", bodies(behind))
	}

	// One side of the fork covers itself and what it was made after, and nothing else.
	behind, err = l.Since([]ID{left.ID()})
	if err != nil {
		t.Fatalf("Since(): %v", err)
	}
	if !same(bodies(behind), []string{"right", "join"}) {
		t.Fatalf("Since(left) = %v", bodies(behind))
	}

	behind, err = l.Since(l.Heads())
	if err != nil {
		t.Fatalf("Since(): %v", err)
	}
	if len(behind) != 0 {
		t.Fatalf("Since(heads) = %v, want nothing", bodies(behind))
	}

	// Somebody with nothing takes all of it, in an order they can take it in.
	behind, err = l.Since(nil)
	if err != nil {
		t.Fatalf("Since(): %v", err)
	}
	if len(behind) != 4 || string(behind[0].Body) != "first" {
		t.Fatalf("Since(nothing) = %v", bodies(behind))
	}
}

// A head this log never heard of stands for changes it does not hold, so it covers nothing here.
func TestSinceIgnoresAHeadNobodyHereHasSeen(t *testing.T) {
	asSomebody(t)

	l := aLog(t, "thing")
	first := signed(t, "first")
	add(t, l, first)

	elsewhere := signed(t, "made somewhere else")
	behind, err := l.Since([]ID{elsewhere.ID()})
	if err != nil {
		t.Fatalf("Since(): %v", err)
	}
	if !same(bodies(behind), []string{"first"}) {
		t.Fatalf("Since(a stranger) = %v", bodies(behind))
	}
}

// A crash mid-write truncates the tail. The records before it are still good, and losing them
// would be the worse outcome.
func TestATruncatedRecordDoesNotLoseTheOnesBeforeIt(t *testing.T) {
	asSomebody(t)

	l := aLog(t, "thing")
	first := signed(t, "first")
	second := signed(t, "second", first.ID())
	third := signed(t, "third", second.ID())
	add(t, l, first, second, third)

	raw, err := os.ReadFile(l.file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.file, raw[:len(raw)-20], 0o600); err != nil {
		t.Fatal(err)
	}

	order, err := l.Ordered()
	if err != nil {
		t.Fatalf("Ordered(): %v", err)
	}
	if !same(bodies(order), []string{"first", "second"}) {
		t.Fatalf("Ordered() = %v, want everything before the truncated record", bodies(order))
	}
}

// One damaged record costs one change. Its length says where the next one starts, so the rest of
// the file is still readable.
func TestADamagedRecordCostsOnlyItself(t *testing.T) {
	asSomebody(t)

	l := aLog(t, "thing")
	first := signed(t, "first")
	left := signed(t, "left", first.ID())
	right := signed(t, "right", first.ID())
	add(t, l, first, left, right)

	raw, err := os.ReadFile(l.file)
	if err != nil {
		t.Fatal(err)
	}
	at := starts(t, raw)
	if len(at) != 3 {
		t.Fatalf("the log holds %d records, want 3", len(at))
	}
	raw[at[1]+3] ^= 1
	if err := os.WriteFile(l.file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// A fresh process reads the file rather than what this one is holding.
	l.read = false

	order, err := l.Ordered()
	if err != nil {
		t.Fatalf("Ordered(): %v", err)
	}
	if !same(bodies(order), []string{"first", "right"}) {
		t.Fatalf("Ordered() = %v, want everything but the damaged record", bodies(order))
	}
}

// starts is where each record's body begins, past its length.
func starts(t *testing.T, raw []byte) []int {
	t.Helper()

	var out []int
	for at := 0; at < len(raw); {
		width, used := binary.Uvarint(raw[at:])
		if used <= 0 || at+used+int(width) > len(raw) {
			t.Fatalf("the log does not walk: %d bytes in", at)
		}
		out = append(out, at+used)
		at += used + int(width)
	}
	return out
}

// A change is authored by a person. A machine with no user key at all says so rather than
// authoring it as nobody.
func TestSigningWithNoUserKeySaysSo(t *testing.T) {
	t.Setenv("DROP_USER_KEY", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := Sign([]byte("who said this"), nil); err == nil {
		t.Fatal("a change was signed by nobody")
	} else if !strings.Contains(err.Error(), "no user key") {
		t.Fatalf("Sign() = %v", err)
	}
}

// The heads are written one way, so one set of changes seen is one change and not several
// spellings of it.
func TestAChangeNamingTheSameHeadTwiceIsRefused(t *testing.T) {
	asSomebody(t)

	l := aLog(t, "thing")
	first := signed(t, "first")
	add(t, l, first)

	second := signed(t, "second", first.ID())
	second.Heads = []ID{first.ID(), first.ID()}
	if _, err := l.Add(second); err == nil {
		t.Fatal("a change naming one head twice was taken")
	} else if !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("Add() = %v", err)
	}
}

// A thing's id becomes a directory, so one that would climb out of the history is not a thing's id.
func TestAThingWhoseIDIsAPathIsRefused(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	for _, at := range []string{"", ".", "..", "../elsewhere", "a/b"} {
		if _, err := Open(at); err == nil {
			t.Errorf("Open(%q) was allowed", at)
		}
	}
}
