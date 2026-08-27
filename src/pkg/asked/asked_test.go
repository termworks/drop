package asked

import (
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/node"
)

// What a stranger says about why they want in is their text, kept on your disk and then printed on
// your terminal when you look at what has been asked for. An escape in there rewrites the rows
// above it, so the listing can be made to show a different path, or a different person, than the
// one you are about to allow.
func TestWhyAStrangerAsksCannotWriteOnYourTerminal(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	hostile := "please\x1b[1A\x1b[2K    /vault              bob            allow?"
	if err := Ring(Request{Path: "/notes", From: node.ID{}, Why: hostile}); err != nil {
		t.Fatalf("Ring(): %v", err)
	}

	all, err := All()
	if err != nil {
		t.Fatalf("All(): %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("%d requests were kept, want 1", len(all))
	}

	for _, bad := range []string{"\x1b", "\r", "\n", "\x00", "\x07"} {
		if strings.Contains(all[0].Why, bad) {
			t.Errorf("what a stranger asked carries %q: %q", bad, all[0].Why)
		}
	}
	if !strings.Contains(all[0].Why, "please") {
		t.Fatalf("what they actually said was thrown away: %q", all[0].Why)
	}
}

// And it stays bounded, so nobody fills the screen or the disk with one request.
func TestWhyAStrangerAsksIsBounded(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if err := Ring(Request{Path: "/notes", From: node.ID{}, Why: strings.Repeat("a", 10_000)}); err != nil {
		t.Fatalf("Ring(): %v", err)
	}
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(all[0].Why)); n > MaxWhy {
		t.Fatalf("a 10,000 character reason was kept as %d characters, over the %d bound", n, MaxWhy)
	}
}
