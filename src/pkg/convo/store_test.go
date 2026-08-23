package convo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
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
