package cmd

import (
	"testing"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/user"
)

// asMe makes this test's machine one whose user key is key, with an address book holding one other
// machine of that user's.
func asMe(t *testing.T, key string) book.Entry {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	mine.Lock()
	was := mine.key
	mine.key = key
	mine.Unlock()
	t.Cleanup(func() {
		mine.Lock()
		mine.key = was
		mine.Unlock()
	})

	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	pinned.Pair("tron", idFor(1), filled(1, book.SecretBytes))
	pinned.Belongs("tron", key)
	if err := pinned.Save(); err != nil {
		t.Fatal(err)
	}
	tron, _ := pinned.Lookup("tron")
	return tron
}

func TestAMachineOfMineNamedByAnotherIsWrittenDown(t *testing.T) {
	tron := asMe(t, "ssh-ed25519 mine")
	circle := filled(9, user.CircleSize)

	joinCircle(tron, proto.Hello{Circle: circle, Mine: []proto.Member{
		{ID: idFor(1).String(), Name: "tron"},
		{ID: idFor(2).String(), Name: "Emulator Phone"},
	}})

	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	phone, ok := pinned.ByID(idFor(2))
	if !ok {
		t.Fatal("a machine another of mine named was not written down")
	}
	if !phone.Circle || phone.User != "ssh-ed25519 mine" || phone.Name != "emulator-phone" || !phone.Paired() {
		t.Fatalf("written down as %+v", phone)
	}
	if len(pinned.All()) != 2 {
		t.Fatalf("the book holds %d machines, want tron and the one it named", len(pinned.All()))
	}
}

func TestAStrangersListOfMachinesIsNotBelieved(t *testing.T) {
	asMe(t, "ssh-ed25519 mine")
	stranger := book.Entry{Name: "mallory", ID: idFor(7), User: "ssh-ed25519 theirs"}

	joinCircle(stranger, proto.Hello{Circle: filled(9, user.CircleSize), Mine: []proto.Member{
		{ID: idFor(3).String(), Name: "not-yours"},
	}})

	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pinned.ByID(idFor(3)); ok {
		t.Fatal("a machine a stranger named was written down as mine")
	}
}

func filled(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// A machine of mine taken out is marked, forgotten, and turned away as a stranger, whatever badge
// it still wears.
func TestAMachineOfMineTakenOutIsAStranger(t *testing.T) {
	tron := asMe(t, "ssh-ed25519 mine")

	if err := forgetKnown("tron", true); err != nil {
		t.Fatal(err)
	}
	if !user.Removed(tron.ID.String()) {
		t.Fatal("taking a machine out left no mark")
	}
	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pinned.ByID(tron.ID); ok {
		t.Fatal("a machine taken out is still in the book")
	}
	who := whoIs(pinned)(tron.ID, proto.Badged{Key: "ssh-ed25519 mine", As: "tron"}, proto.Stood{})
	if who.UserName != "" || who.Paired || who.Trusted {
		t.Fatalf("a machine taken out was still taken for %+v", who)
	}
}

// A machine another of mine took out is forgotten here too, and not written back in by the next
// machine that still names it.
func TestAMarkFromAnotherMachineOfMineTakesItOutHere(t *testing.T) {
	tron := asMe(t, "ssh-ed25519 mine")
	circle := filled(9, user.CircleSize)
	phone := idFor(2)

	joinCircle(tron, proto.Hello{Circle: circle, Mine: []proto.Member{{ID: phone.String(), Name: "phone"}}})
	joinCircle(tron, proto.Hello{Circle: circle, Gone: []proto.Mark{{ID: phone.String(), At: 100, Gone: true}}})

	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pinned.ByID(phone); ok {
		t.Fatal("a machine another of mine took out is still here")
	}

	joinCircle(tron, proto.Hello{Circle: circle, Mine: []proto.Member{{ID: phone.String(), Name: "phone"}}})
	if err := pinned.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, ok := pinned.ByID(phone); ok {
		t.Fatal("a machine taken out was written back in by one that still named it")
	}
}

// Your own machines are never taken for a person, whatever the first of them was called: forgetting
// one must not forget the rest.
func TestForgettingOneMachineOfMineLeavesTheRest(t *testing.T) {
	asMe(t, "ssh-ed25519 mine")
	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := pinned.Change(func() (bool, error) {
		pinned.Pair("phone", idFor(2), filled(2, book.SecretBytes))
		pinned.Belongs("phone", "ssh-ed25519 mine")
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := forgetKnown("tron", true); err != nil {
		t.Fatal(err)
	}
	if err := pinned.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, ok := pinned.Lookup("phone"); !ok {
		t.Fatal("forgetting one machine of mine forgot another")
	}
}
