package book

import (
	"os"
	"sync"
	"testing"
)

// Two things changing the book at once must not lose each other's work.
//
// Read, change, write is three steps, and a writer that lands between the first and the third has
// its change thrown away by the third. This matters more than it used to: a machine saying it moved
// makes the daemon write, so the moment is chosen by somebody else.
func TestTwoWritersDoNotLoseEachOther(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	first.Pair("orin", testID(t), testSecret(t))
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	// Two books over one file, the way the daemon and `drop peer pair` are two processes.
	daemon, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	moved, laptop := testID(t), testID(t)
	was, _ := daemon.Lookup("orin")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = daemon.Change(func() (bool, error) {
			_, ok := daemon.Moved(was.ID, moved)
			return ok, nil
		})
	}()
	go func() {
		defer wg.Done()
		_ = pairing.Change(func() (bool, error) {
			pairing.Pair("laptop", laptop, testSecret(t))
			return true, nil
		})
	}()
	wg.Wait()

	// Whatever order they ran in, the file holds both.
	after, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, held := after.Lookup("laptop"); !held {
		t.Error("the pairing was lost by the machine that moved")
	}
	orin, held := after.Lookup("orin")
	if !held {
		t.Fatal("the moved machine is gone from the book")
	}
	if orin.ID != moved {
		t.Errorf("orin is %v, and it moved to %v", orin.ID, moved)
	}
	if !orin.Paired() {
		t.Error("the moved machine lost the secret it shared")
	}
}

func TestRememberingAnAddressKeepsANewerPairing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	orin := testID(t)
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	first.Pair("orin", orin, testSecret(t))
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	learning, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	laptop := testID(t)
	if err := pairing.Change(func() (bool, error) {
		pairing.Pair("laptop", laptop, testSecret(t))
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if changed, err := learning.Reached(orin, "192.168.1.9:47777"); err != nil || !changed {
		t.Fatalf("Reached() = %v, %v", changed, err)
	}

	after, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Lookup("laptop"); !ok {
		t.Fatal("remembering the address overwrote the newer pairing")
	}
	if entry, ok := after.Lookup("orin"); !ok || len(entry.Addrs) != 1 {
		t.Fatalf("orin after remembering = %+v, %v", entry, ok)
	}
}

// A change that changed nothing must not rewrite the file: most connections carry a handover
// nobody here has an entry for, and every one of them would otherwise be a write.
func TestAChangeThatChangedNothingWritesNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b.Pair("orin", testID(t), testSecret(t))
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	at, err := path()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(at)
	if err != nil {
		t.Fatal(err)
	}

	// A handover for a machine nobody here has heard of.
	if err := b.Change(func() (bool, error) {
		_, ok := b.Moved(testID(t), testID(t))
		return ok, nil
	}); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(at)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("a change that changed nothing rewrote the address book")
	}
}
