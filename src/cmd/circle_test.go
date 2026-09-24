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
