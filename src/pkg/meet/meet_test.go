package meet

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/user"
	"github.com/bresilla/drop/src/pkg/wire"
)

// asSomebody gives this machine a user key to sign changes with, and answers what it signs as.
func asSomebody(t *testing.T) string {
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

	key, err := user.Public()
	if err != nil {
		t.Fatalf("reading the user key back: %v", err)
	}
	return user.Text(key)
}

// aLog is one thing's record, in a data directory of its own.
func aLog(t *testing.T, at string) *history.Log {
	t.Helper()

	t.Setenv("XDG_DATA_HOME", t.TempDir())
	l, err := history.Open(at)
	if err != nil {
		t.Fatalf("Open(%q): %v", at, err)
	}
	return l
}

func signed(t *testing.T, body string, heads ...history.ID) history.Change {
	t.Helper()

	c, err := history.Sign([]byte(body), heads)
	if err != nil {
		t.Fatalf("Sign(%q): %v", body, err)
	}
	return c
}

func add(t *testing.T, l *history.Log, changes ...history.Change) {
	t.Helper()

	for _, c := range changes {
		if _, err := l.Add(c); err != nil {
			t.Fatalf("Add(%q): %v", c.Body, err)
		}
	}
}

func bodies(t *testing.T, l *history.Log) []string {
	t.Helper()

	order, err := l.Ordered()
	if err != nil {
		t.Fatalf("Ordered(): %v", err)
	}
	out := make([]string, 0, len(order))
	for _, c := range order {
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

// anybody admits every author, which is what a namespace shared with everybody it names does.
func anybody(string) bool { return true }

// meeting runs both halves over a pair of pipes and hands back what each side came to.
func meeting(t *testing.T, mine, theirs *history.Log, admits func(string) bool) (Caught, Caught) {
	t.Helper()

	here, there := net.Pipe()
	defer here.Close()
	defer there.Close()

	var (
		wg              sync.WaitGroup
		asked, answered Caught
		askErr, ansErr  error
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		asked, askErr = Ask(wire.NewConn(here), mine, admits)
	}()
	go func() {
		defer wg.Done()
		answered, ansErr = Answer(wire.NewConn(there), theirs, admits)
	}()
	wg.Wait()

	if askErr != nil {
		t.Fatalf("Ask(): %v", askErr)
	}
	if ansErr != nil {
		t.Fatalf("Answer(): %v", ansErr)
	}
	return asked, answered
}

// The whole reason the package exists: two machines change one thing while they cannot see each
// other, and when they meet both hold both changes, in the same order.
func TestTwoMachinesThatBothChangedSomethingEndUpWithBoth(t *testing.T) {
	asSomebody(t)

	// The change they were both made after, which each of them already has.
	first := signed(t, "first")
	mine, theirs := aLog(t, "thing"), aLog(t, "other")
	add(t, mine, first)
	add(t, theirs, first)

	// And then one each, out of sight of the other.
	here := signed(t, "written here", first.ID())
	there := signed(t, "written there", first.ID())
	add(t, mine, here)
	add(t, theirs, there)

	asked, answered := meeting(t, mine, theirs, anybody)

	if asked.Taken != 1 || answered.Taken != 1 {
		t.Fatalf("one change each way, got %d and %d", asked.Taken, answered.Taken)
	}

	ours, yours := bodies(t, mine), bodies(t, theirs)
	if !same(ours, yours) {
		t.Fatalf("the two read differently: %v and %v", ours, yours)
	}
	if len(ours) != 3 {
		t.Fatalf("both should hold three changes, got %v", ours)
	}
}

// Running it again says nothing, because a change already held is written down again as nothing.
func TestMeetingTwiceTakesNothingTheSecondTime(t *testing.T) {
	asSomebody(t)

	first := signed(t, "first")
	mine, theirs := aLog(t, "thing"), aLog(t, "other")
	add(t, mine, first, signed(t, "and then", first.ID()))
	add(t, theirs, first)

	if _, answered := meeting(t, mine, theirs, anybody); answered.Taken != 1 {
		t.Fatalf("the first meeting took %d changes, want 1", answered.Taken)
	}

	asked, answered := meeting(t, mine, theirs, anybody)
	if asked.Sent != 0 || answered.Taken != 0 {
		t.Fatalf("the second meeting sent %d and took %d, want nothing either way", asked.Sent, answered.Taken)
	}
}

// A machine with nothing takes the lot, in an order it can take it in.
func TestAMachineWithNothingTakesAllOfIt(t *testing.T) {
	asSomebody(t)

	first := signed(t, "first")
	left := signed(t, "left", first.ID())
	right := signed(t, "right", first.ID())
	join := signed(t, "join", left.ID(), right.ID())

	mine, theirs := aLog(t, "thing"), aLog(t, "other")
	add(t, mine, first, left, right, join)

	if _, answered := meeting(t, mine, theirs, anybody); answered.Taken != 4 {
		t.Fatalf("took %d changes, want all four", answered.Taken)
	}
	if !same(bodies(t, mine), bodies(t, theirs)) {
		t.Fatalf("the two read differently: %v and %v", bodies(t, mine), bodies(t, theirs))
	}
}

// Deciding that a signature is really somebody's is the history's business. Deciding whether that
// somebody was allowed to change this thing is the access rule's, and it is asked here.
func TestAChangeFromSomebodyTheRuleDoesNotAdmitIsRefused(t *testing.T) {
	stranger := asSomebody(t)

	first := signed(t, "first")
	mine, theirs := aLog(t, "thing"), aLog(t, "other")
	add(t, mine, first, signed(t, "not shared with them", first.ID()))

	nobody := func(author string) bool { return author != stranger }

	_, answered := meeting(t, mine, theirs, nobody)
	if answered.Taken != 0 {
		t.Fatalf("took %d changes from somebody who is not admitted", answered.Taken)
	}
	if answered.Refused != 2 {
		t.Fatalf("refused %d changes, want both of them", answered.Refused)
	}
	if held := bodies(t, theirs); len(held) != 0 {
		t.Fatalf("the log holds %v", held)
	}
}

// A namespace with nobody admitted takes nothing, because a rule that admits nobody is what an
// undeclared one is.
func TestAMeetingWithNoRuleTakesNothing(t *testing.T) {
	asSomebody(t)

	mine, theirs := aLog(t, "thing"), aLog(t, "other")
	add(t, mine, signed(t, "first"))

	_, answered := meeting(t, mine, theirs, nil)
	if answered.Taken != 0 || answered.Refused != 1 {
		t.Fatalf("took %d and refused %d, want nothing taken", answered.Taken, answered.Refused)
	}
}

// A change made after one that was passed over is passed over too: it cannot be placed in an order
// without what it names, and the meeting carries on rather than ending on somebody else's fault.
func TestAChangeAfterARefusedOneIsPassedOver(t *testing.T) {
	stranger := asSomebody(t)

	first := signed(t, "first")
	second := signed(t, "second", first.ID())

	mine, theirs := aLog(t, "thing"), aLog(t, "other")
	add(t, mine, first, second)

	// The first change is theirs already, so only what follows it can be refused.
	add(t, theirs, first)

	nobody := func(author string) bool { return author != stranger }
	if _, answered := meeting(t, mine, theirs, nobody); answered.Refused != 1 {
		t.Fatalf("refused %d changes, want the one after the refusal", answered.Refused)
	}
	if held := bodies(t, theirs); !same(held, []string{"first"}) {
		t.Fatalf("the log holds %v", held)
	}
}

// The far end's number of heads is the far end's number, so a huge one is refused rather than
// allocated for.
func TestTooManyHeadsAreRefused(t *testing.T) {
	here, there := net.Pipe()
	defer here.Close()
	defer there.Close()

	go func() {
		w := wire.NewWriter()
		w.Uint(uint64(history.MaxHeads) + 1)
		_ = wire.NewConn(there).WriteFrame(wire.KindItem, w.Body())
	}()

	if _, err := readHeads(wire.NewConn(here)); err == nil {
		t.Fatal("a claim of too many heads was believed")
	} else if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("readHeads() = %v", err)
	}
}
