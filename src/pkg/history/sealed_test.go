package history

import (
	"bytes"
	"encoding/binary"
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

func TestASealedChangeCannotCarryUnauthenticatedBytes(t *testing.T) {
	asSomebody(t)
	key := aKey(t)
	c := about(t, "one", "what alice wrote")
	kept, err := seal(key, record(c), "one", c.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unseal(key, append(kept, 0), "one"); err == nil {
		t.Fatal("unseal() accepted trailing bytes outside the authenticated body")
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

func TestRewriteSealsAndClearsTheSameSignedChanges(t *testing.T) {
	asSomebody(t)
	Unlock(nil)
	t.Cleanup(func() { Unlock(nil) })

	l := aLog(t, thing)
	first := signed(t, "first plaintext secret")
	second := signed(t, "second plaintext secret", first.ID())
	add(t, l, first, second)
	want, err := l.Ordered()
	if err != nil {
		t.Fatal(err)
	}

	key := bytes.Repeat([]byte{9}, 32)
	Unlock(key)
	if err := l.Rewrite(key); err != nil {
		t.Fatalf("Rewrite(key): %v", err)
	}
	assertRecordState(t, l.file, true, "first plaintext secret", "second plaintext secret")
	assertSameChanges(t, l, want)

	if err := l.Rewrite(nil); err != nil {
		t.Fatalf("Rewrite(nil): %v", err)
	}
	assertRecordState(t, l.file, false)
	assertSameChanges(t, l, want)
}

func TestRewriteMakesAMixedLogUniformAndIsRepeatable(t *testing.T) {
	asSomebody(t)
	Unlock(nil)
	t.Cleanup(func() { Unlock(nil) })

	l := aLog(t, thing)
	first := signed(t, "clear record")
	add(t, l, first)

	key := bytes.Repeat([]byte{11}, 32)
	Unlock(key)
	second := signed(t, "sealed record", first.ID())
	add(t, l, second)
	raw, err := os.ReadFile(l.file)
	if err != nil {
		t.Fatal(err)
	}
	records := storedRecordBodies(t, raw)
	if len(records) != 2 || isSealed(records[0]) || !isSealed(records[1]) {
		t.Fatalf("mixed record states were not preserved before rewriting")
	}

	want, err := l.Ordered()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := l.Rewrite(key); err != nil {
			t.Fatalf("Rewrite(key) pass %d: %v", i+1, err)
		}
		assertRecordState(t, l.file, true, "clear record", "sealed record")
		assertSameChanges(t, l, want)
	}
}

func TestRewriteLeavesAbsentAndEmptyLogsAlone(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "empty"}[present], func(t *testing.T) {
			Unlock(nil)
			l := aLog(t, thing)
			if present {
				if err := os.WriteFile(l.file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := l.Rewrite(bytes.Repeat([]byte{13}, 32)); err != nil {
				t.Fatalf("Rewrite(): %v", err)
			}
			_, err := os.Stat(l.file)
			if present && err != nil {
				t.Fatalf("empty log disappeared: %v", err)
			}
			if !present && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("absent log was created: %v", err)
			}
		})
	}
}

func TestRewriteFailureLeavesTheExistingLogUntouched(t *testing.T) {
	asSomebody(t)
	Unlock(nil)
	t.Cleanup(func() { Unlock(nil) })

	l := aLog(t, thing)
	add(t, l, signed(t, "stays intact"))
	want, err := os.ReadFile(l.file)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Rewrite([]byte("not a data key")); err == nil {
		t.Fatal("Rewrite() accepted an invalid destination key")
	}
	got, err := os.ReadFile(l.file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("a failed rewrite changed the log")
	}
}

func assertSameChanges(t *testing.T, l *Log, want []Change) {
	t.Helper()
	got, err := l.Ordered()
	if err != nil {
		t.Fatalf("Ordered(): %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Ordered() has %d changes, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].Encode(), want[i].Encode()) {
			t.Fatalf("change %d changed while rewriting", i)
		}
	}
}

func assertRecordState(t *testing.T, file string, sealed bool, absent ...string) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range absent {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("%s still contains %q", file, secret)
		}
	}
	for i, body := range storedRecordBodies(t, raw) {
		if isSealed(body) != sealed {
			t.Fatalf("record %d sealed = %v, want %v", i, isSealed(body), sealed)
		}
	}
}

func storedRecordBodies(t *testing.T, raw []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for len(raw) > 0 {
		if !bytes.HasPrefix(raw, mark) {
			t.Fatalf("record does not begin with %q", mark)
		}
		raw = raw[len(mark):]
		width, used := binary.Uvarint(raw)
		if used <= 0 || width > uint64(len(raw)-used) {
			t.Fatal("record has an invalid length")
		}
		raw = raw[used:]
		out = append(out, raw[:int(width)])
		raw = raw[int(width):]
	}
	return out
}
