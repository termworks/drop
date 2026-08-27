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

// thing is the one both machines hold. A change made about anything else belongs in another
// history and is refused there, so a meeting is always about one thing.
const thing = "thing"

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
func aLog(t *testing.T) *history.Log {
	t.Helper()

	t.Setenv("XDG_DATA_HOME", t.TempDir())
	l, err := history.Open(thing)
	if err != nil {
		t.Fatalf("Open(%q): %v", thing, err)
	}
	return l
}

func signed(t *testing.T, body string, heads ...history.ID) history.Change {
	t.Helper()

	c, err := history.Sign(thing, []byte(body), heads)
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
		asked, askErr = Ask(wire.NewConn(here), mine, "them", admits)
	}()
	go func() {
		defer wg.Done()
		answered, ansErr = Answer(wire.NewConn(there), theirs, "us", admits)
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
	mine, theirs := aLog(t), aLog(t)
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
	mine, theirs := aLog(t), aLog(t)
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

	mine, theirs := aLog(t), aLog(t)
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
	mine, theirs := aLog(t), aLog(t)
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

	mine, theirs := aLog(t), aLog(t)
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

	mine, theirs := aLog(t), aLog(t)
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

// One change the history will not take costs itself, not the meeting. A machine that has such a
// record re-offers it at every meeting it ever has, so ending the meeting on it would stop that
// namespace converging with everybody, for good.
func TestAChangeTheHistoryRefusesDoesNotEndTheMeeting(t *testing.T) {
	asSomebody(t)

	first := signed(t, "first")
	second := signed(t, "second", first.ID())

	// The same change with a signature that is not over it: it decodes, and it will not verify.
	spoiled := signed(t, "damaged since it was written")
	spoiled.Signed = append([]byte(nil), signed(t, "something else entirely").Signed...)

	l := aLog(t)
	caught, err := answering(t, l, first, spoiled, second)
	if err != nil {
		t.Fatalf("Answer(): %v", err)
	}
	if caught.Taken != 2 {
		t.Fatalf("took %d changes, want the two either side of the damaged one", caught.Taken)
	}
	if caught.Refused != 1 || caught.Trouble == nil {
		t.Fatalf("refused %d changes and said %v about it, want one and a reason", caught.Refused, caught.Trouble)
	}
	if held := bodies(t, l); !same(held, []string{"first", "second"}) {
		t.Fatalf("the log holds %v", held)
	}
}

// A change made about another thing is one this history has no business taking, whoever signed it
// and whoever the rule admits.
func TestAChangeAboutAnotherThingIsRefusedRatherThanTaken(t *testing.T) {
	asSomebody(t)

	elsewhere, err := history.Sign("another", []byte("what alice wrote about the other thing"), nil)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}

	l := aLog(t)
	caught, err := answering(t, l, elsewhere, signed(t, "first"))
	if err != nil {
		t.Fatalf("Answer(): %v", err)
	}
	if caught.Taken != 1 || caught.Refused != 1 {
		t.Fatalf("took %d and refused %d, want the stray one refused", caught.Taken, caught.Refused)
	}
	if held := bodies(t, l); !same(held, []string{"first"}) {
		t.Fatalf("the log holds %v", held)
	}
}

// answering runs the answering half against changes written straight down the wire, which is how a
// peer that is not drop itself would send them.
func answering(t *testing.T, l *history.Log, changes ...history.Change) (Caught, error) {
	t.Helper()

	here, there := net.Pipe()
	defer here.Close()
	defer there.Close()

	go func() {
		conn := wire.NewConn(there)
		w := wire.NewWriter()
		w.Uint(0)
		_ = conn.WriteFrame(wire.KindItem, w.Body())
		if _, _, err := conn.ReadFrame(); err != nil {
			return
		}
		for _, c := range changes {
			_ = conn.WriteFrame(wire.KindItem, c.Encode())
		}
		_ = conn.WriteFrame(wire.KindEnd, wire.End{Size: int64(len(changes))}.Encode())
		for {
			if _, _, err := conn.ReadFrame(); err != nil {
				return
			}
		}
	}()

	return Answer(wire.NewConn(here), l, "them", anybody)
}

// What the far end said it held is remembered, because that is what decides whether a history can
// be folded away. A peer that has caught up and then falls behind again holds the fold off.
func TestAMeetingRemembersHowFarTheFarEndGot(t *testing.T) {
	asSomebody(t)

	mine, theirs := aLog(t), aLog(t)
	var heads []history.ID
	for i := range history.Least + 1 {
		c := signed(t, string(rune('a'+i%26)), heads...)
		add(t, mine, c)
		heads = []history.ID{c.ID()}
	}

	// The heads are said before anything is sent, so it is the meeting after the one that caught
	// them up that says they are caught up.
	meeting(t, mine, theirs, anybody)
	if mine.Folding() {
		t.Fatal("a history was folded away on what a peer said before it took anything")
	}

	meeting(t, mine, theirs, anybody)
	if !mine.Folding() {
		t.Fatal("a history everybody has caught up on was not worth folding")
	}

	add(t, mine, signed(t, "written since they left", mine.Heads()...))
	if mine.Folding() {
		t.Fatal("a history was folded away with a peer behind on the last change")
	}
}
