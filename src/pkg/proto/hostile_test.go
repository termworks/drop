package proto

import (
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/metal"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/plate"
)

// A terminal is an interpreter, not a display. Everything a peer says about itself gets printed,
// so a peer that puts an escape in it is writing on somebody else's screen — moving the cursor up
// and rewriting a row it never sent, so its own entry can claim to be read-only while the row it
// overwrote said otherwise.
func TestAPeerCannotWriteOnYourTerminal(t *testing.T) {
	hostile := "a chat\x1b[1A\x1b[2K  /secrets  files  read and write"

	said := Hello{
		Name:    "\x1b]0;not drop\x07innocent",
		Version: "0.3.2\r\nowned",
		Serves: []Served{{
			Path:      "/chat\x1b[2J",
			Archetype: "chat\x00",
			About:     hostile,
		}},
	}

	back, err := decodeHello(said.encode())
	if err != nil {
		t.Fatalf("decodeHello(): %v", err)
	}

	for _, got := range []string{back.Name, back.Version, back.Serves[0].Path,
		back.Serves[0].Archetype, back.Serves[0].About} {
		for _, bad := range []string{"\x1b", "\r", "\n", "\x00", "\x07"} {
			if strings.Contains(got, bad) {
				t.Errorf("%q reached a listing carrying %q", got, bad)
			}
		}
	}

	// And what is left is still the honest part of what they said.
	if !strings.Contains(back.Serves[0].About, "a chat") {
		t.Fatalf("the real text was thrown away with the escape: %q", back.Serves[0].About)
	}
}

// A peer must not be able to push the rest of a listing off the screen either.
func TestAPeerCannotFillTheScreen(t *testing.T) {
	said := Hello{
		Name:   strings.Repeat("n", 40_000),
		Serves: []Served{{Path: "/" + strings.Repeat("p", 40_000), About: strings.Repeat("a", 40_000)}},
	}

	back, err := decodeHello(said.encode())
	if err != nil {
		t.Fatalf("decodeHello(): %v", err)
	}
	if n := len([]rune(back.Name)); n > 200 {
		t.Errorf("a 40,000 character node name came back as %d characters", n)
	}
	if n := len([]rune(back.Serves[0].About)); n > 200 {
		t.Errorf("a 40,000 character description came back as %d characters", n)
	}
	if n := len([]rune(back.Serves[0].Path)); n > 300 {
		t.Errorf("a 40,000 character path came back as %d characters", n)
	}
}

// A stamp is signed, so it cannot be quietly cleaned up on the way in — what was verified and what
// is shown have to be the same string. A machine that signs a name full of escapes is refused
// outright instead.
func TestAStampNamingSomethingUnprintableIsRefused(t *testing.T) {
	if !metal.Read().Held() {
		t.Skip("this machine says nothing about itself, so it can stamp nothing")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := time.Now()

	stamp, sig, err := plate.Sign(now)
	if err != nil {
		t.Fatal(err)
	}

	// It signs and reads back as it stands.
	if _, err := plate.Read(stamp.Bytes(), sig, now); err != nil {
		t.Fatalf("an ordinary stamp was refused: %v", err)
	}

	// With an escape in the account it is not a stamp, signature or no signature.
	for _, bad := range []string{"root\x1b[1A", strings.Repeat("a", 300), "a‮b", ""} {
		nasty := stamp
		nasty.Whose = bad
		if _, err := plate.Read(nasty.Bytes(), sig, now); err == nil {
			t.Errorf("a stamp naming %q was believed", bad)
		}
	}
}

// The two signed fields are a few hundred bytes at the outside. Sixty-four kilobytes each of
// somebody else's choosing, parsed before anybody is authenticated, is work drop should not do.
func TestSomethingSignedIsBoundedOnTheWire(t *testing.T) {
	open := Opening{
		Path:    "/notes",
		Plate:   []byte(strings.Repeat("x", MaxSigned+1)),
		Stamped: make([]byte, MaxSignature),
	}
	if _, err := decodeOpen(open.encode()); err == nil {
		t.Fatal("an oversized plate was read off the wire")
	}

	open = Opening{
		Path:   "/notes",
		Moved:  []byte(strings.Repeat("x", 16)),
		Handed: make([]byte, MaxSignature+1),
	}
	if _, err := decodeOpen(open.encode()); err == nil {
		t.Fatal("an oversized signature was read off the wire")
	}

	// And what is the right size still goes through.
	open = Opening{Path: "/notes", Plate: []byte("drop-plate/1\n"), Stamped: make([]byte, MaxSignature)}
	if _, err := decodeOpen(open.encode()); err != nil {
		t.Fatalf("an ordinary opening was refused: %v", err)
	}
}

// Rubbish in the two new fields must cost a length check, not a parse, and must never be mistaken
// for a machine that moved.
func TestRubbishInAHandoverMovesNothing(t *testing.T) {
	here, err := node.LocalID()
	if err != nil {
		t.Fatal(err)
	}

	for what, open := range map[string]Opening{
		"nothing":                 {},
		"noise":                   {Moved: []byte(strings.Repeat("\x00", 512)), Handed: make([]byte, 64)},
		"a plate, not a handover": {Moved: []byte("drop-plate/1\nmachine x\n"), Handed: make([]byte, 64)},
		"a short signature":       {Moved: []byte("drop-handover/1\n"), Handed: []byte("short")},
	} {
		if was, ok := handed(here, open); ok || !was.IsZero() {
			t.Errorf("%s was read as a machine that moved: %v", what, was)
		}
	}
}

// Who else holds a namespace is a list the far end writes, and it is printed when you look at
// joining one. It is the last thing a peer says about itself, and it gets the same treatment.
func TestTheHolderListCannotWriteOnYourTerminal(t *testing.T) {
	said := Hello{
		Name:   "orin",
		Serves: []Served{{Path: "/notes", Holders: []string{"ssh-ed25519 AAAA\x1b[2Jowned"}}},
	}

	back, err := decodeHello(said.encode())
	if err != nil {
		t.Fatalf("decodeHello(): %v", err)
	}
	for _, who := range back.Serves[0].Holders {
		for _, bad := range []string{"\x1b", "\r", "\n", "\x00"} {
			if strings.Contains(who, bad) {
				t.Errorf("a holder reached a listing carrying %q: %q", bad, who)
			}
		}
	}

	// A name longer than any key could be makes the whole message unreadable rather than being cut
	// down to size. A sender is expected to cut its own lists — one that sends a name a thousand
	// times longer than a key is not a sender whose listing is worth showing.
	huge := Hello{Serves: []Served{{Path: "/x", Holders: []string{strings.Repeat("k", 40_000)}}}}
	if _, err := decodeHello(huge.encode()); err == nil {
		t.Fatal("a holder name of 40,000 characters was read off the wire")
	}

	// An ordinary key is untouched, because a key is printable already.
	ordinary := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIbk9rJ4ec3MxEHxE2Grpt bob@laptop"
	one, err := decodeHello(Hello{Serves: []Served{{Path: "/x", Holders: []string{ordinary}}}}.encode())
	if err != nil {
		t.Fatal(err)
	}
	if got := one.Serves[0].Holders[0]; got != ordinary {
		t.Fatalf("an ordinary key came back as %q", got)
	}
}
