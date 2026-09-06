package convo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/keep"
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

func TestConversationDirectoriesAreBounded(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := prepareConversation(root, filepath.Join(root, "three"), 2); err == nil {
		t.Fatal("a conversation was created past the directory limit")
	}
	if err := prepareConversation(root, filepath.Join(root, "one"), 2); err != nil {
		t.Fatalf("an existing conversation was refused: %v", err)
	}
}

func TestConversationDirectoryCannotBeASymlink(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(root, "peer")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := prepareConversation(root, link, 2); err == nil {
		t.Fatal("a symlink was accepted as a conversation directory")
	}
}

func TestConversationDirectoryLimitIsConcurrent(t *testing.T) {
	root := t.TempDir()
	const (
		limit      = 8
		contenders = 32
	)

	start := make(chan struct{})
	results := make(chan error, contenders)
	var waiting sync.WaitGroup
	waiting.Add(contenders)
	for i := range contenders {
		go func() {
			waiting.Done()
			<-start
			results <- prepareConversation(root, filepath.Join(root, fmt.Sprintf("peer-%d", i)), limit)
		}()
	}
	waiting.Wait()
	close(start)

	created := 0
	for range contenders {
		if err := <-results; err == nil {
			created++
		}
	}
	if created != limit {
		t.Fatalf("created %d conversation directories, want %d", created, limit)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	directories := 0
	for _, entry := range entries {
		if entry.IsDir() {
			directories++
		}
	}
	if directories != limit {
		t.Fatalf("found %d conversation directories, want %d", directories, limit)
	}
}

func TestConversationStorageIsBoundedAcrossPeers(t *testing.T) {
	root := t.TempDir()
	for _, peer := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(root, peer), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	body := []byte("one record")
	var head [binary.MaxVarintLen64]byte
	frameBytes := int64(binary.PutUvarint(head[:], uint64(len(body))) + len(body))
	if err := appendToLimit(filepath.Join(root, "one", "history"), body, frameBytes); err != nil {
		t.Fatalf("writing within the account limit: %v", err)
	}
	if err := appendToLimit(filepath.Join(root, "two", "history"), body, frameBytes); err == nil {
		t.Fatal("conversation storage grew past the account limit")
	}
	if _, err := os.Stat(filepath.Join(root, "two", "history")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused log was created: %v", err)
	}
}

func TestConversationStorageLimitIsConcurrent(t *testing.T) {
	root := t.TempDir()
	for _, peer := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(root, peer), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	body := []byte("one record")
	var head [binary.MaxVarintLen64]byte
	frameBytes := int64(binary.PutUvarint(head[:], uint64(len(body))) + len(body))
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, peer := range []string{"one", "two"} {
		go func() {
			<-start
			results <- appendToLimit(filepath.Join(root, peer, "history"), body, frameBytes)
		}()
	}
	close(start)

	written := 0
	for range 2 {
		if err := <-results; err == nil {
			written++
		}
	}
	if written != 1 {
		t.Fatalf("%d concurrent logs were written, want 1", written)
	}
	used, err := conversationBytes(root, MaxConversations+2)
	if err != nil {
		t.Fatal(err)
	}
	if used != frameBytes {
		t.Fatalf("conversation storage is %d bytes, want %d", used, frameBytes)
	}
}

func TestConversationStorageRejectsSymlinkedLogs(t *testing.T) {
	root := t.TempDir()
	peer := filepath.Join(root, "peer")
	if err := os.Mkdir(peer, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(peer, "history")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if err := appendToLimit(filepath.Join(peer, "outbox"), []byte("message"), 1024); err == nil {
		t.Fatal("conversation storage accepted a symlinked log")
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "untouched" {
		t.Fatalf("symlink target = %q, %v", raw, err)
	}
}

func TestConversationRewriteCannotExceedAccountLimit(t *testing.T) {
	root := t.TempDir()
	peer := filepath.Join(root, "peer")
	if err := os.Mkdir(peer, 0o700); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(peer, "history")
	before := []byte("old")
	if err := os.WriteFile(history, before, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := replaceConversationLog(history, []byte("larger"), int64(len(before))); err == nil {
		t.Fatal("a rewrite grew conversation storage past the account limit")
	}
	after, err := os.ReadFile(history)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("a refused account rewrite changed the original log")
	}
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

func TestQueueIsIdempotent(t *testing.T) {
	s := openStore(t)
	m, err := New(KindText, "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(m); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(m); err != nil {
		t.Fatal(err)
	}

	waiting, err := s.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 {
		t.Fatalf("Pending() = %d copies, want 1", len(waiting))
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

func TestARewriteCannotGrowALogPastItsLimit(t *testing.T) {
	s := openStore(t)
	queue(t, s, "kept in the original log")

	before, err := os.ReadFile(s.history)
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteLog(s.history, s.peer.String(), make([]byte, 32), 1); err == nil {
		t.Fatal("a rewritten log grew past its limit")
	}
	after, err := os.ReadFile(s.history)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("a refused rewrite changed the original log")
	}
}

func TestOutboxChangesWaitForOtherProcesses(t *testing.T) {
	s := openStore(t)
	m := queue(t, s, "waiting")

	locked := make(chan struct{})
	release := make(chan struct{})
	lockErr := make(chan error, 1)
	go func() {
		lockErr <- keep.While(s.outbox, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	done := make(chan error, 1)
	go func() { done <- s.Delivered(m.ID) }()
	select {
	case err := <-done:
		t.Fatalf("Delivered() passed a held cross-process lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-lockErr; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
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

func TestAnOversizedConversationLogIsRefused(t *testing.T) {
	s := openStore(t)
	if err := os.WriteFile(s.history, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(s.history, MaxLog+1); err != nil {
		t.Fatal(err)
	}

	if _, err := s.History(); err == nil {
		t.Fatal("History() accepted an oversized log")
	}
	m, err := New(KindText, "one more", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(m); err == nil {
		t.Fatal("Add() grew an oversized log")
	}
	stat, err := os.Stat(s.history)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() != MaxLog+1 {
		t.Fatalf("oversized log changed to %d bytes", stat.Size())
	}
}

func TestConversationLogsRefuseHardLinks(t *testing.T) {
	s := openStore(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, s.history); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}

	m, err := New(KindText, "private", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(m); err == nil {
		t.Fatal("Add() wrote through a hard link")
	}
	stat, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() != 0 {
		t.Fatalf("hard-link target changed to %d bytes", stat.Size())
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

func TestRewriteDoesNotFollowAPredictableScratchLink(t *testing.T) {
	s := openStore(t)
	queue(t, s, "kept")

	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, s.history+".new"); err != nil {
		t.Fatal(err)
	}

	if err := s.Rewrite(nil); err != nil {
		t.Fatalf("Rewrite(): %v", err)
	}
	if raw, err := os.ReadFile(victim); err != nil || string(raw) != "untouched" {
		t.Fatalf("scratch link target = %q, %v", raw, err)
	}
	stat, err := os.Lstat(s.history)
	if err != nil {
		t.Fatal(err)
	}
	if !stat.Mode().IsRegular() {
		t.Fatalf("rewritten history mode = %v", stat.Mode())
	}
	history, err := s.History()
	if err != nil || len(history) != 1 || history[0].Body != "kept" {
		t.Fatalf("rewritten history = %+v, %v", history, err)
	}
}

// Storing a message must not cost a read of everything said before it. The daemon opens the
// conversation once per arriving message, so the walk has to be paid once, not per message.
func TestStoringDoesNotRereadTheWholeLog(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	peer := testPeer(t)
	body := strings.Repeat("x", 4096)

	var s *Store
	for range 1600 {
		var err error
		s, err = Open(peer)
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

	if s.reads != 1 {
		t.Fatalf("storing 1600 messages read the whole log %d times", s.reads)
	}
}

func TestDedupeNoticesAnEqualSizedReplacement(t *testing.T) {
	s := openStore(t)
	first := Message{ID: "first-id", Kind: KindText, Dir: In, Body: "first", At: 1}
	if fresh, err := s.Add(first); err != nil || !fresh {
		t.Fatalf("Add(first) = %v, %v", fresh, err)
	}

	second := Message{ID: "other-id", Kind: KindText, Dir: In, Body: "other", At: 2}
	body, err := record(second, s.peer.String())
	if err != nil {
		t.Fatal(err)
	}
	var head [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(head[:], uint64(len(body)))
	replacement := append(append([]byte{}, head[:n]...), body...)
	if stat, err := os.Stat(s.history); err != nil {
		t.Fatal(err)
	} else if int64(len(replacement)) != stat.Size() {
		t.Fatalf("replacement is %d bytes, want %d", len(replacement), stat.Size())
	}
	if err := keep.Replace(s.history, replacement); err != nil {
		t.Fatal(err)
	}

	if fresh, err := s.Add(first); err != nil || !fresh {
		t.Fatalf("Add(first) after replacement = %v, %v", fresh, err)
	}
	history, err := s.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("History() = %d messages, want both records", len(history))
	}
}
