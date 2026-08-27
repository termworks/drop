package proto

import (
	"bytes"
	"encoding/binary"
	"github.com/bresilla/drop/src/pkg/wire"
	"io"
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

// Who else holds a namespace is a list of user keys the far end writes. It is bounded rather than
// cleaned, and the difference is the point: these are never printed as they arrive. Each is looked
// up in the address book and what gets shown is the local name for whoever it turns out to be, or a
// count of the people it did not recognise. So the danger here is not a terminal, it is a peer
// claiming a thousand names of sixty-four kilobytes each and making this machine read them all.
func TestTheHolderListIsBoundedToWhatAKeyCouldBe(t *testing.T) {
	huge := Hello{Serves: []Served{{Path: "/x", Holders: []string{strings.Repeat("k", 40_000)}}}}
	if _, err := decodeHello(huge.encode()); err == nil {
		t.Fatal("a holder name of 40,000 characters was read off the wire")
	}

	// A real key goes through exactly as it is, trailing newline and all: it is matched against
	// what this machine computes for the same person, and a byte of difference is a stranger.
	ordinary := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIbk9rJ4ec3MxEHxE2Grpt bob@laptop\n"
	one, err := decodeHello(Hello{Serves: []Served{{Path: "/x", Holders: []string{ordinary}}}}.encode())
	if err != nil {
		t.Fatal(err)
	}
	if got := one.Serves[0].Holders[0]; got != ordinary {
		t.Fatalf("a key came back as %q, want %q", got, ordinary)
	}
}

// The size of a frame is a number the sender chooses, and the first frame is read from somebody
// nobody has decided anything about. A few bytes claiming the largest frame there is must not buy
// the whole of it: that is a stranger naming how much of this machine's memory to set aside.
func TestAStrangerCannotNameHowMuchMemoryToSetAside(t *testing.T) {
	head := header(wire.KindOpen, wire.MaxFrame)
	if len(head) > 16 {
		t.Fatalf("a header claiming %d bytes is itself %d bytes", wire.MaxFrame, len(head))
	}

	conn := wire.NewConn(readOnly{bytes.NewReader(head)})
	if _, _, err := conn.ReadFrameUpTo(MaxUnknown); err == nil {
		t.Fatalf("%d bytes bought a %d byte frame before anybody was authenticated", len(head), wire.MaxFrame)
	}

	// What an opening can legally be still goes through.
	big := Opening{Path: "/notes", Secret: strings.Repeat("s", 1000), Badge: make([]byte, 60000)}
	body := big.encode()
	if len(body) > MaxUnknown {
		t.Fatalf("an opening this node would send is %d bytes, over its own %d bound", len(body), MaxUnknown)
	}
	conn = wire.NewConn(readOnly{bytes.NewReader(append(header(wire.KindOpen, len(body)), body...))})
	if _, got, err := conn.ReadFrameUpTo(MaxUnknown); err != nil || len(got) != len(body) {
		t.Fatalf("an ordinary opening was refused: %v", err)
	}
}

// header is a frame header as it goes on the wire: the kind, then the length as a varint.
func header(kind byte, size int) []byte {
	out := make([]byte, 1, 16)
	out[0] = kind
	return binary.AppendUvarint(out, uint64(size))
}

// readOnly is a stream that only ever reads, for a reader being fed bytes that never answers.
type readOnly struct{ io.Reader }

func (readOnly) Write(p []byte) (int, error) { return len(p), nil }
func (readOnly) Close() error                { return nil }
