package cmd

import (
	stdbytes "bytes"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/user"
)

func loaded(t *testing.T) *book.Book {
	t.Helper()
	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	return pinned
}

// Somebody paired from another machine of mine is known here too, with the secret that finds them.
func TestAPersonPairedElsewhereIsKnownHere(t *testing.T) {
	asMe(t, "ssh-ed25519 mine")
	secret := filled(7, book.SecretBytes)

	changed, err := takeState(loaded(t), synced{Entries: []syncedEntry{
		{Name: "bob-laptop", ID: idFor(5).String(), Secret: secret, User: "ssh-ed25519 bob", Person: "bob", At: 10},
	}})
	if err != nil || !changed {
		t.Fatalf("taking a person from another machine changed %v (%v)", changed, err)
	}
	got, ok := loaded(t).ByID(idFor(5))
	if !ok || got.Person != "bob" || !stdbytes.Equal(got.Secret, secret) {
		t.Fatalf("bob arrived as %+v", got)
	}
}

// Of two words about one machine the newer stands, whichever arrives first.
func TestTheNewerWordAboutAMachineStands(t *testing.T) {
	asMe(t, "ssh-ed25519 mine")
	entry := syncedEntry{Name: "bob-laptop", ID: idFor(5).String(), Secret: filled(7, book.SecretBytes), User: "ssh-ed25519 bob", Person: "bob", At: 20}
	if _, err := takeState(loaded(t), synced{Entries: []syncedEntry{entry}}); err != nil {
		t.Fatal(err)
	}

	older := entry
	older.Name, older.Trusted, older.At = "old-name", true, 10
	if _, err := takeState(loaded(t), synced{Entries: []syncedEntry{older}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := loaded(t).ByID(idFor(5)); got.Name != "bob-laptop" || got.Trusted {
		t.Fatalf("an older word overwrote a newer one: %+v", got)
	}

	newer := entry
	newer.Name, newer.Person, newer.Trusted, newer.At = "robert-laptop", "robert", true, 30
	if _, err := takeState(loaded(t), synced{Entries: []syncedEntry{newer}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := loaded(t).ByID(idFor(5)); got.Name != "robert-laptop" || got.Person != "robert" || !got.Trusted {
		t.Fatalf("a newer word did not stand: %+v", got)
	}
}

// Whatever is removed on another machine of mine is removed here, and not written back in by a
// machine that still holds the older word about it.
func TestARemovalElsewhereIsARemovalHere(t *testing.T) {
	asMe(t, "ssh-ed25519 mine")
	entry := syncedEntry{Name: "bob-laptop", ID: idFor(5).String(), Secret: filled(7, book.SecretBytes), User: "ssh-ed25519 bob", Person: "bob", At: 20}
	if _, err := takeState(loaded(t), synced{Entries: []syncedEntry{entry}}); err != nil {
		t.Fatal(err)
	}

	gone := synced{Marks: map[string]user.Mark{idFor(5).String(): {At: time.Now().UnixNano(), Gone: true}}}
	if _, err := takeState(loaded(t), gone); err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded(t).ByID(idFor(5)); ok {
		t.Fatal("a machine removed elsewhere is still here")
	}

	if _, err := takeState(loaded(t), synced{Entries: []syncedEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded(t).ByID(idFor(5)); ok {
		t.Fatal("an older word wrote a removed machine back in")
	}
}

// A machine of mine that arrives is found under the secret this machine works out with it from the
// circle, never under the one the machine that named it holds.
func TestAMachineOfMineArrivesWithItsOwnPairSecret(t *testing.T) {
	asMe(t, "ssh-ed25519 mine")
	circle := filled(9, user.CircleSize)
	if _, err := user.AdoptCircle(circle); err != nil {
		t.Fatal(err)
	}

	if _, err := takeState(loaded(t), synced{Entries: []syncedEntry{
		{Name: "phone", ID: idFor(6).String(), User: "ssh-ed25519 mine", Person: "tron", At: 10},
	}}); err != nil {
		t.Fatal(err)
	}
	got, ok := loaded(t).ByID(idFor(6))
	if !ok || !got.Circle || !got.Paired() {
		t.Fatalf("a machine of mine arrived as %+v", got)
	}
}

// Two machines called the same are two entries, not one overwriting the other.
func TestAMachineWithATakenNameGetsAnother(t *testing.T) {
	asMe(t, "ssh-ed25519 mine")
	if _, err := takeState(loaded(t), synced{Entries: []syncedEntry{
		{Name: "tron", ID: idFor(8).String(), Secret: filled(3, book.SecretBytes), User: "ssh-ed25519 carol", Person: "carol", At: 10},
	}}); err != nil {
		t.Fatal(err)
	}
	pinned := loaded(t)
	if mine, _ := pinned.Lookup("tron"); mine.ID != idFor(1) {
		t.Fatalf("my own tron was overwritten by %+v", mine)
	}
	if carol, ok := pinned.Lookup("tron-2"); !ok || carol.ID != idFor(8) {
		t.Fatalf("carol's tron was not filed beside it: %+v", carol)
	}
}

// Forgetting somebody's machine marks it, so it goes from every machine of mine.
func TestForgettingSomebodyMarksThem(t *testing.T) {
	asMe(t, "ssh-ed25519 mine")
	if _, err := takeState(loaded(t), synced{Entries: []syncedEntry{
		{Name: "bob-laptop", ID: idFor(5).String(), Secret: filled(7, book.SecretBytes), User: "ssh-ed25519 bob", Person: "bob", At: 10},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := forgetKnown("bob", true); err != nil {
		t.Fatal(err)
	}
	if !user.Removed(idFor(5).String()) {
		t.Fatal("forgetting somebody left no mark for the rest of my machines")
	}
	if held := bookToHand(loaded(t)); len(held.Marks) == 0 {
		t.Fatal("the mark is not handed to the rest of my machines")
	}
}
