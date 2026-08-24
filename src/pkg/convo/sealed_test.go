package convo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
)

func peerFor(seed byte) node.ID {
	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

// locked runs a body with a data key held, and puts it back afterwards so one test cannot leave
// another encrypting.
func locked(t *testing.T, key []byte, body func()) {
	t.Helper()

	was, _ := keyed()
	Unlock(key)
	defer Unlock(was)

	body()
}

func aKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

// Without a key, `strings` reads the history. With one, it does not -- and drop still does.
func TestASealedHistoryIsNotReadableWithStrings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	peer := peerFor(1)

	locked(t, aKey(), func() {
		store, err := Open(peer)
		if err != nil {
			t.Fatal(err)
		}
		m, err := New(KindText, "the eagle has landed", "")
		if err != nil {
			t.Fatal(err)
		}
		m.Dir = Out
		if _, err := store.Add(m); err != nil {
			t.Fatal(err)
		}

		raw, err := os.ReadFile(filepath.Join(dir, "drop", "convo", peer.String(), "history"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "the eagle has landed") {
			t.Error("the message is on disk in the clear")
		}

		back, err := store.History()
		if err != nil {
			t.Fatal(err)
		}
		if len(back) != 1 || back[0].Body != "the eagle has landed" {
			t.Errorf("read back %+v", back)
		}
		if back[0].Dir != Out {
			t.Error("the direction did not survive being sealed")
		}
	})
}

// Turning a vault on must not hide what was written before it. Both forms read.
func TestAHistoryWrittenBeforeTheVaultStillReads(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	peer := peerFor(2)
	store, err := Open(peer)
	if err != nil {
		t.Fatal(err)
	}

	before, err := New(KindText, "written in the clear", "")
	if err != nil {
		t.Fatal(err)
	}
	before.Dir = In
	if _, err := store.Add(before); err != nil {
		t.Fatal(err)
	}

	locked(t, aKey(), func() {
		after, err := New(KindText, "written sealed", "")
		if err != nil {
			t.Fatal(err)
		}
		after.Dir = Out
		if _, err := store.Add(after); err != nil {
			t.Fatal(err)
		}

		back, err := store.History()
		if err != nil {
			t.Fatal(err)
		}
		if len(back) != 2 {
			t.Fatalf("read back %d records, wanted both", len(back))
		}
		if back[0].Body != "written in the clear" || back[1].Body != "written sealed" {
			t.Errorf("read back %+v", back)
		}
	})
}

// With the key gone the history is there and unreadable, and that is what has to be said.
func TestALockedHistoryIsNotAnEmptyOne(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	peer := peerFor(3)

	locked(t, aKey(), func() {
		store, _ := Open(peer)
		m, _ := New(KindText, "something", "")
		m.Dir = Out
		if _, err := store.Add(m); err != nil {
			t.Fatal(err)
		}
	})

	store, err := Open(peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.History(); !errors.Is(err, ErrLocked) {
		t.Fatalf("a locked history came back with %v", err)
	}
}

// A record is tied to its conversation and to itself: moving one into another conversation, or
// putting one back after it was taken out, has to fail rather than quietly work.
func TestASealedRecordCannotBeMoved(t *testing.T) {
	key := aKey()

	sealedUp, err := seal(key, []byte("the body"), "peer-one", "message-one")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := unseal(key, sealedUp, "peer-one"); err != nil {
		t.Fatalf("a record would not open in its own conversation: %v", err)
	}
	if _, err := unseal(key, sealedUp, "peer-two"); err == nil {
		t.Error("a record opened in somebody else's conversation")
	}
}

// Turning a vault on encrypts what is already there, and turning it off puts it back.
func TestAHistoryCanBeSealedAndUnsealedAfterTheFact(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	peer := peerFor(4)
	at := filepath.Join(dir, "drop", "convo", peer.String(), "history")

	store, err := Open(peer)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"first", "second", "third"} {
		m, err := New(KindText, body, "")
		if err != nil {
			t.Fatal(err)
		}
		m.Dir = Out
		if _, err := store.Add(m); err != nil {
			t.Fatal(err)
		}
	}

	inTheClear := func(want bool) {
		t.Helper()

		raw, err := os.ReadFile(at)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(raw), "second"); got != want {
			t.Errorf("in the clear on disk = %v, wanted %v", got, want)
		}
	}
	inTheClear(true)

	// Sealing: written in the clear, read in the clear, written back under a key.
	if err := store.Rewrite(aKey()); err != nil {
		t.Fatal(err)
	}
	inTheClear(false)

	locked(t, aKey(), func() {
		back, err := store.History()
		if err != nil {
			t.Fatal(err)
		}
		if len(back) != 3 {
			t.Fatalf("sealing lost records: %d left", len(back))
		}

		// And back again: read under the key, written back without one.
		if err := store.Rewrite(nil); err != nil {
			t.Fatal(err)
		}
	})

	inTheClear(true)
	back, err := store.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 3 {
		t.Fatalf("unsealing lost records: %d left", len(back))
	}
}
