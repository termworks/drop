package history

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

// aKey is the data key a vault would have unwrapped, held for one test.
func aKey(t *testing.T) []byte {
	t.Helper()

	key := bytes.Repeat([]byte{7}, 32)
	Unlock(key)
	t.Cleanup(func() { Unlock(nil) })
	return key
}

// A history holds somebody's notes and the contents of the folders they share. Without a key,
// `strings` reads it. With one, it does not — and drop still does.
func TestASealedLogDoesNotReadInTheClear(t *testing.T) {
	asSomebody(t)

	secret := "the thing alice wrote down"
	clear := aLog(t, thing)
	add(t, clear, signed(t, secret))

	raw, err := os.ReadFile(clear.file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(secret)) {
		t.Fatal("a log with no key should be in the clear")
	}

	aKey(t)
	l := aLog(t, thing)
	add(t, l, signed(t, secret))

	raw, err = os.ReadFile(l.file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("a sealed log reads in the clear")
	}

	l.read = false
	if held := read(t, l); !same(held, []string{secret}) {
		t.Fatalf("Ordered() = %v, want what was written", held)
	}
}

// A record is bound to the thing it belongs to, so one lifted out of another thing's log is not a
// record here — the same binding the signature makes, said again by the seal.
func TestASealedRecordCannotBeMovedToAnotherThingsLog(t *testing.T) {
	asSomebody(t)
	key := aKey(t)

	c := about(t, "one", "what alice wrote about the first thing")
	kept, err := stored(record(c), "one", c.ID())
	if err != nil {
		t.Fatalf("stored(): %v", err)
	}
	if !isSealed(kept) {
		t.Fatal("a record was kept in the clear with a key held")
	}

	if _, err := unseal(key, kept, "one"); err != nil {
		t.Fatalf("unseal() where it belongs: %v", err)
	}
	if _, err := unseal(key, kept, "another"); err == nil {
		t.Fatal("a record was opened in another thing's history")
	}
}

// A whole log carried into another thing's history is nothing there, rather than that thing's
// history rewritten with somebody else's words.
func TestALogCarriedIntoAnotherThingReadsAsNothing(t *testing.T) {
	asSomebody(t)
	aKey(t)

	one := aLog(t, "one")
	add(t, one, about(t, "one", "what alice wrote about the first thing"))
	raw, err := os.ReadFile(one.file)
	if err != nil {
		t.Fatal(err)
	}

	two := aLog(t, "two")
	if err := os.WriteFile(two.file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if held := read(t, two); len(held) != 0 {
		t.Fatalf("Ordered() = %v, want nothing", held)
	}
}

// A sealed history on a device whose key is not held is there and unreadable, which is a different
// thing from empty. Saying it is empty would be a lie about the disk.
func TestASealedLogWithoutTheKeyIsNotAnEmptyOne(t *testing.T) {
	asSomebody(t)
	aKey(t)

	l := aLog(t, thing)
	add(t, l, signed(t, "the thing alice wrote down"))

	Unlock(nil)
	l.read = false
	if _, err := l.Ordered(); !errors.Is(err, ErrLocked) {
		t.Fatalf("Ordered() = %v, want the device to say it is locked", err)
	}
}
