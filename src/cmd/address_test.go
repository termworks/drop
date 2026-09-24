package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/ns"
)

// booked writes an address book of this test's own, so resolving reads a book nobody else shares.
//
// Straight to the file rather than through Pair and Belongs: what is being tested is reading a
// book somebody already has, and the label a person is filed under is one of the things a case
// here wants to choose.
func booked(t *testing.T, machines map[string]string) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	onDisk, seed := map[string]map[string]any{}, byte(1)
	for name, person := range machines {
		one := map[string]any{
			"id":     idFor(seed).String(),
			"secret": base64.StdEncoding.EncodeToString(make([]byte, book.SecretBytes)),
		}
		if person != "" {
			one["user"] = "ssh-ed25519 " + strings.ToUpper(person)
			one["person"] = person
		}
		onDisk[name] = one
		seed++
	}

	raw, err := json.MarshalIndent(onDisk, "", "  ")
	if err != nil {
		t.Fatalf("encoding the book: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "drop"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drop", "peers.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// One machine of theirs and no machine named is not ambiguous: there is only one answer.
func TestAUserWithOneMachineResolvesToIt(t *testing.T) {
	booked(t, map[string]string{"laptop": "bob"})

	at, err := ns.ParseAddress("bob::/chat")
	if err != nil {
		t.Fatalf("ParseAddress(): %v", err)
	}
	entry, err := resolve(t.Context(), at)
	if err != nil {
		t.Fatalf("resolve(): %v", err)
	}
	if entry.Name != "laptop" {
		t.Errorf("resolved to %q", entry.Name)
	}
}

// With several machines and none named, the answer is which ones there are rather than a guess.
func TestAUserWithSeveralMachinesIsNotGuessedAt(t *testing.T) {
	booked(t, map[string]string{"laptop": "bob", "phone": "bob"})

	at, err := ns.ParseAddress("bob::/chat")
	if err != nil {
		t.Fatalf("ParseAddress(): %v", err)
	}

	_, err = resolve(t.Context(), at)
	if err == nil {
		t.Fatal("one of two machines was picked silently")
	}
	for _, want := range []string{"laptop", "phone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is not named in %q", want, err)
		}
	}
}

func conflictingPersonBook(t *testing.T) *book.Book {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	pinned, err := book.Load()
	if err != nil {
		t.Fatal(err)
	}
	pinned.Pair("alice", idFor(24), make([]byte, book.SecretBytes))
	pinned.Belongs("alice", aliceKey)
	pinned.Pair("desktop", idFor(25), make([]byte, book.SecretBytes))
	pinned.Belongs("desktop", aliceKey)
	pinned.Remove("alice")
	pinned.Pair("alice", idFor(26), make([]byte, book.SecretBytes))
	pinned.Belongs("alice", carolKey)
	if err := pinned.Save(); err != nil {
		t.Fatal(err)
	}
	return pinned
}

func TestConflictingPersonNamesDoNotResolve(t *testing.T) {
	pinned := conflictingPersonBook(t)

	at, err := ns.ParseAddress("alice::/chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(t.Context(), at); err == nil || !strings.Contains(err.Error(), "more than one person") {
		t.Fatalf("resolving conflicting people returned %v", err)
	}
	theirs, err := machinesOf(pinned, aliceKey)
	if err != nil || len(theirs) != 1 || theirs[0].Name != "desktop" {
		t.Fatalf("resolving by user key = %+v, %v", theirs, err)
	}
}

// A name that is neither in the book nor a peer id is nothing to dial.
func TestAMachineTheBookDoesNotHaveIsRefused(t *testing.T) {
	booked(t, map[string]string{"laptop": "bob"})

	at, err := ns.ParseAddress("desktop:/chat")
	if err != nil {
		t.Fatalf("ParseAddress(): %v", err)
	}
	if _, err := resolve(t.Context(), at); err == nil {
		t.Fatal("a machine nobody has heard of resolved")
	}
}

// This machine is not somebody to dial, and saying so is better than reaching for a wire.
func TestThisMachineIsNotResolvedToAPeer(t *testing.T) {
	booked(t, map[string]string{"laptop": "bob"})

	at, err := ns.ParseAddress("/chat")
	if err != nil {
		t.Fatalf("ParseAddress(): %v", err)
	}
	if !at.Here {
		t.Fatal("/chat is not this machine")
	}

	_, err = resolve(t.Context(), at)
	if err == nil {
		t.Fatal("this machine resolved to an entry in the address book")
	}
	if !strings.Contains(err.Error(), "this machine") {
		t.Errorf("the refusal does not say why: %q", err)
	}
}

// Below a directory namespace the rest of the path is a filename on the far machine, so it keeps
// whatever capitals and spaces were typed.
func TestAFilenameUnderAnAddressIsLeftAlone(t *testing.T) {
	cases := []struct{ text, machine, under string }{
		{"orin:/work/Report Two.pdf", "orin", "/work/Report Two.pdf"},
		{"bob:laptop:/work/deep", "laptop", "/work/deep"},
		{"orin", "orin", "/"},
		{"/shared/Notes", "", "/shared/Notes"},
	}

	for _, c := range cases {
		at, under, err := splitAddress(c.text)
		if err != nil {
			t.Errorf("splitAddress(%q): %v", c.text, err)
			continue
		}
		if at.Machine != c.machine || under != c.under {
			t.Errorf("splitAddress(%q) = %q %q, want %q %q", c.text, at.Machine, under, c.machine, c.under)
		}
	}
}

func TestHeldReplyMustFinishBeforeItNarrowsAnAddress(t *testing.T) {
	alice, bob := idFor(21), idFor(22)
	complete := fmt.Sprintf("%s\n%s\n%s\n", alice, bob, heldReplyEnd)

	held, err := readHeldReply(strings.NewReader(complete))
	if err != nil || !held[alice] || !held[bob] || len(held) != 2 {
		t.Fatalf("complete reply = %v, %v", held, err)
	}

	partial := fmt.Sprintf("%s\n", alice)
	if held, err := readHeldReply(strings.NewReader(partial)); err == nil || held != nil {
		t.Fatalf("partial reply = %v, %v", held, err)
	}
}

func TestHeldReplyRefusesMalformedDevices(t *testing.T) {
	held, err := readHeldReply(strings.NewReader("not-a-device\n" + heldReplyEnd + "\n"))
	if err == nil || held != nil {
		t.Fatalf("malformed reply = %v, %v", held, err)
	}
}

func TestHeldReplyReportsReaderFailure(t *testing.T) {
	held, err := readHeldReply(io.MultiReader(strings.NewReader(idFor(23).String()+"\n"), brokenReader{}))
	if err == nil || held != nil {
		t.Fatalf("broken reply = %v, %v", held, err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }
