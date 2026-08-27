package convo

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

func testPeer(t *testing.T) node.ID {
	t.Helper()

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	return sk.Public().EndpointID()
}

func openStore(t *testing.T) *Store {
	t.Helper()

	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s, err := Open(testPeer(t))
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	return s
}

func queue(t *testing.T, s *Store, body string) Message {
	t.Helper()

	m, err := New(KindText, body, "")
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := s.Queue(m); err != nil {
		t.Fatalf("Queue(): %v", err)
	}
	return m
}

// A message is in the history the moment it is composed. What is uncertain is whether it arrived.
func TestQueuedMessageIsAlreadyHistory(t *testing.T) {
	s := openStore(t)
	queue(t, s, "hello")

	history, err := s.History()
	if err != nil {
		t.Fatalf("History(): %v", err)
	}
	if len(history) != 1 || history[0].Body != "hello" {
		t.Fatalf("History() = %+v", history)
	}

	waiting, err := s.Pending()
	if err != nil {
		t.Fatalf("Pending(): %v", err)
	}
	if len(waiting) != 1 {
		t.Fatalf("Pending() = %d messages, want 1", len(waiting))
	}
}

// Only what the far end confirmed leaves the outbox; a partial delivery is retried, not lost.
func TestDeliveredClearsOnlyWhatWasConfirmed(t *testing.T) {
	s := openStore(t)
	first := queue(t, s, "one")
	queue(t, s, "two")
	third := queue(t, s, "three")

	if err := s.Delivered(first.ID, third.ID); err != nil {
		t.Fatalf("Delivered(): %v", err)
	}

	waiting, err := s.Pending()
	if err != nil {
		t.Fatalf("Pending(): %v", err)
	}
	if len(waiting) != 1 || waiting[0].Body != "two" {
		t.Fatalf("Pending() = %+v, want only \"two\"", waiting)
	}

	// Clearing the outbox must never touch the history.
	history, _ := s.History()
	if len(history) != 3 {
		t.Fatalf("History() lost entries: %d, want 3", len(history))
	}
}

func TestDeliveredEverythingRemovesTheOutbox(t *testing.T) {
	s := openStore(t)
	m := queue(t, s, "only one")

	if err := s.Delivered(m.ID); err != nil {
		t.Fatalf("Delivered(): %v", err)
	}
	waiting, _ := s.Pending()
	if len(waiting) != 0 {
		t.Fatalf("Pending() = %d, want 0", len(waiting))
	}
}

// A resend must not appear twice, or every reconnect duplicates the backlog.
func TestAddIsIdempotent(t *testing.T) {
	s := openStore(t)

	m, _ := New(KindText, "said once", "")
	m.Dir = In

	fresh, err := s.Add(m)
	if err != nil || !fresh {
		t.Fatalf("first Add() = %v, %v", fresh, err)
	}
	fresh, err = s.Add(m)
	if err != nil {
		t.Fatalf("second Add(): %v", err)
	}
	if fresh {
		t.Fatal("Add() reported a resend as new, so it would be acted on twice")
	}

	history, _ := s.History()
	if len(history) != 1 {
		t.Fatalf("History() = %d entries, want 1", len(history))
	}
}

// A crash mid-write truncates the tail. Everything written before it must still read back.
func TestTruncatedTailDoesNotLoseEarlierMessages(t *testing.T) {
	s := openStore(t)
	queue(t, s, "first")
	queue(t, s, "second")

	raw, err := os.ReadFile(s.history)
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}
	// Half a record, as an interrupted append would leave.
	if err := os.WriteFile(s.history, append(raw, 0x40, 0x01, 0x02), 0o600); err != nil {
		t.Fatalf("truncating: %v", err)
	}

	history, err := s.History()
	if err != nil {
		t.Fatalf("History() failed on a truncated log: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("History() = %d entries, want the 2 written before the tear", len(history))
	}
}

func TestHistoryIsOrderedByTime(t *testing.T) {
	s := openStore(t)

	for _, body := range []string{"a", "b", "c"} {
		m, _ := New(KindText, body, "")
		if _, err := s.Add(m); err != nil {
			t.Fatalf("Add(): %v", err)
		}
	}

	history, _ := s.History()
	for i := 1; i < len(history); i++ {
		if history[i-1].At > history[i].At {
			t.Fatal("History() is not in time order")
		}
	}
}

func TestKindsAndBodiesSurvive(t *testing.T) {
	s := openStore(t)

	want := []struct {
		kind byte
		body string
		more string
	}{
		{KindText, "a line of text", ""},
		{KindLink, "https://example.com/a?b=c#d", ""},
		{KindFile, "holiday.zip", "3.0 MB"},
		{KindEvent, "cast started", "tty"},
	}
	for _, w := range want {
		m, _ := New(w.kind, w.body, w.more)
		if _, err := s.Add(m); err != nil {
			t.Fatalf("Add(): %v", err)
		}
	}

	history, _ := s.History()
	if len(history) != len(want) {
		t.Fatalf("History() = %d entries, want %d", len(history), len(want))
	}
	for i, w := range want {
		if history[i].Kind != w.kind || history[i].Body != w.body || history[i].Extra != w.more {
			t.Fatalf("entry %d came back as %+v", i, history[i])
		}
	}
}

func TestPeersListsConversations(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)

	id := testPeer(t)
	s, err := Open(id)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	queue(t, s, "hello")

	found, err := Peers()
	if err != nil {
		t.Fatalf("Peers(): %v", err)
	}
	if len(found) != 1 || found[0] != id {
		t.Fatalf("Peers() = %v, want [%s]", found, id)
	}

	// A stray file where a peer directory should be is skipped, not fatal.
	if err := os.WriteFile(filepath.Join(base, "drop", "convo", "not-a-peer"), []byte("x"), 0o600); err != nil {
		t.Fatalf("writing a stray file: %v", err)
	}
	if found, err = Peers(); err != nil || len(found) != 1 {
		t.Fatalf("Peers() = %v, %v after a stray file", found, err)
	}
}

func TestMessageEncodingRoundTrips(t *testing.T) {
	m, err := New(KindLink, "https://example.com", "extra")
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	back, err := Decode(m.Encode())
	if err != nil {
		t.Fatalf("Decode(): %v", err)
	}
	if back.ID != m.ID || back.Kind != m.Kind || back.Body != m.Body || back.Extra != m.Extra || back.At != m.At {
		t.Fatalf("round trip gave %+v, want %+v", back, m)
	}
}

// Ids order by time so a log sorts lexically, and never collide.
func TestNewIDIsSortableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	last := ""

	for i := 0; i < 500; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID(): %v", err)
		}
		if seen[id] {
			t.Fatal("NewID() collided")
		}
		seen[id] = true
		if id < last {
			t.Fatal("NewID() went backwards; a log would sort out of order")
		}
		last = id
	}
}

// poison is a record as the log used to accept one: whole, correctly framed, and larger than
// anything that reads it will hand back.
func poison(t *testing.T) []byte {
	t.Helper()

	m, err := New(KindText, strings.Repeat("b", MaxBody), strings.Repeat("e", wire.MaxString))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	w := wire.NewWriter()
	w.Byte(In)
	w.Bytes(m.Encode())
	body := w.Body()

	var head [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(head[:], uint64(len(body)))
	return append(head[:n], body...)
}

// A message too large to read back must not reach the log at all, from either direction: a peer
// sending one, or one composed here.
func TestAnOversizedMessageIsRefused(t *testing.T) {
	s := openStore(t)

	big, err := New(KindText, strings.Repeat("b", MaxBody), strings.Repeat("e", wire.MaxString))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if _, err := Decode(big.Encode()); err == nil {
		t.Fatal("Decode() admitted a message larger than the log can hand back")
	}
	if _, err := s.Add(big); err == nil {
		t.Fatal("Add() stored a message larger than the log can hand back")
	}

	queue(t, s, "still fine")
	history, err := s.History()
	if err != nil {
		t.Fatalf("History(): %v", err)
	}
	if len(history) != 1 || history[0].Body != "still fine" {
		t.Fatalf("History() = %+v, want only the message that fits", history)
	}
}

// One record that will not read costs that record and nothing else. Ending the walk there hid
// every message written after it, and the next Rewrite deleted them.
func TestARecordThatWillNotReadDoesNotHideTheRest(t *testing.T) {
	s := openStore(t)
	queue(t, s, "said before")

	raw, err := os.ReadFile(s.history)
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}
	if err := os.WriteFile(s.history, append(raw, poison(t)...), 0o600); err != nil {
		t.Fatalf("writing the history: %v", err)
	}

	queue(t, s, "said after")

	history, err := s.History()
	if err != nil {
		t.Fatalf("History(): %v", err)
	}
	if len(history) != 2 || history[0].Body != "said before" || history[1].Body != "said after" {
		t.Fatalf("History() = %+v, want both messages around the bad record", history)
	}

	// And rewriting the log keeps them, rather than dropping everything past the bad record.
	if err := s.Rewrite(nil); err != nil {
		t.Fatalf("Rewrite(): %v", err)
	}
	if again, _ := s.History(); len(again) != 2 {
		t.Fatalf("History() = %d entries after a rewrite, want 2", len(again))
	}
}

// Storing a message must not cost a read of everything said before it. The daemon opens the
// conversation once per arriving message, so the walk has to be paid once, not per message.
func TestStoringDoesNotRereadTheWholeLog(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	peer := testPeer(t)
	body := strings.Repeat("x", 4096)

	const block = 400
	store := func() time.Duration {
		start := time.Now()
		for i := 0; i < block; i++ {
			s, err := Open(peer)
			if err != nil {
				t.Fatalf("Open(): %v", err)
			}
			m, err := New(KindText, body, "")
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			m.Dir = In
			if _, err := s.Add(m); err != nil {
				t.Fatalf("Add(): %v", err)
			}
		}
		return time.Since(start)
	}

	first := store()
	store()
	store()
	last := store()

	if last > 4*first && last > 200*time.Millisecond {
		t.Fatalf("messages %d-%d took %s against %s for the first %d: the cost grows with the log",
			3*block, 4*block, last, first, block)
	}
}
