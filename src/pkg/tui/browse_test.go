package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/proto"
)

// walkTo opens a namespace on the one paired device by name, whatever kind of namespace it is.
func walkTo(t *testing.T, back *fake, want string) Model {
	t.Helper()

	m := intoPeer(t, start(t, back), 0)
	for i, item := range m.list.Items() {
		if it, ok := item.(pathItem); ok && it.step.at == want {
			m.list.Select(i)
		}
	}
	return settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
}

// withFiles is a device sharing one directory, with a directory inside it.
func withFiles(writable bool) *fake {
	when := time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

	return &fake{
		peers: []book.Entry{
			{Name: "beta", ID: idFor(2), Secret: make([]byte, book.SecretBytes)},
		},
		serves: map[string][]proto.Served{
			"beta": {{Path: "/work", Archetype: "files", Writable: writable, About: "a directory, to walk through"}},
		},
		holding: map[string][]Held{
			"/work": {
				{Name: "reports", Dir: true, At: when},
				{Name: "notes.txt", Size: 4096, At: when},
			},
			"/work/reports": {
				{Name: "july.pdf", Size: 3 << 20, At: when},
			},
		},
	}
}

// A files namespace is a directory, so entering it is a level of its own rather than a screen.
func TestAFilesNamespaceIsWalkedAtItsOwnLevel(t *testing.T) {
	m := walkTo(t, withFiles(false), "/work")

	if m.at != levelBrowse {
		t.Fatalf("entering a files namespace went to level %d", m.at)
	}

	shown := m.View()
	for _, want := range []string{"reports", "notes.txt", "4.0 kB", "directory"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the listing is missing %q:\n%s", want, shown)
		}
	}
}

// Enter walks in, esc walks up, and esc from the root leaves the level.
func TestWalkingInAndBackOut(t *testing.T) {
	back := withFiles(false)
	m := walkTo(t, back, "/work")

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.dir != "reports" {
		t.Fatalf("entering a directory left the level standing at %q", m.dir)
	}
	if !strings.Contains(m.View(), "july.pdf") {
		t.Errorf("what is in the directory was not listed:\n%s", m.View())
	}

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.at != levelBrowse || m.dir != "" {
		t.Fatalf("going back left the level at %d, standing at %q", m.at, m.dir)
	}

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.at != levelPaths {
		t.Fatalf("going back from the root left the level at %d", m.at)
	}

	// A file is not a way down, so entering one does nothing at all.
	m = walkTo(t, back, "/work")
	m.list.Select(1)
	if m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter}); m.dir != "" {
		t.Errorf("entering a file walked into %q", m.dir)
	}
}

// One namespace has as many listings as it has directories. An answer for the one that has been
// left must not land on the one that is on screen.
func TestAListingForAnotherDirectoryIsNotTakenForThisOne(t *testing.T) {
	m := walkTo(t, withFiles(false), "/work")

	m = settle(t, m, heldLoaded{path: "/work", dir: "reports", held: []Held{{Name: "stale.txt"}}})
	if strings.Contains(m.View(), "stale.txt") {
		t.Errorf("a listing for another directory was drawn:\n%s", m.View())
	}

	m = settle(t, m, heldLoaded{path: "/elsewhere", dir: "", held: []Held{{Name: "wrong.txt"}}})
	if strings.Contains(m.View(), "wrong.txt") {
		t.Errorf("a listing for another namespace was drawn:\n%s", m.View())
	}
}

// Everything else here acts on one keystroke. Taking a file off somebody else's disk does not.
func TestRemovingSomethingAsksFirst(t *testing.T) {
	back := withFiles(true)
	m := walkTo(t, back, "/work")
	m.list.Select(1)

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.removing != "notes.txt" {
		t.Fatalf("x did not ask about anything, it asked about %q", m.removing)
	}
	if !strings.Contains(m.View(), "remove notes.txt") {
		t.Errorf("the question was not put on screen:\n%s", m.View())
	}

	// Anything but a yes leaves it where it is.
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if len(back.removedIt) != 0 {
		t.Fatalf("something was removed on a no: %v", back.removedIt)
	}
	if m.removing != "" {
		t.Error("the question stayed on screen after it was answered")
	}

	m.list.Select(1)
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})

	if len(back.removedIt) != 1 || back.removedIt[0] != "/work/notes.txt" {
		t.Fatalf("removed %v", back.removedIt)
	}
	for _, item := range m.list.Items() {
		if it, ok := item.(heldItem); ok && it.held.Name == "notes.txt" {
			t.Errorf("what was removed is still listed:\n%s", m.View())
		}
	}
}

// A download says where it went, because a file that landed somewhere nobody named is lost.
func TestDownloadingSaysWhereItLanded(t *testing.T) {
	back := withFiles(false)
	m := walkTo(t, back, "/work")
	m.list.Select(1)

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})

	if len(back.fetched) != 1 || back.fetched[0] != "/work/notes.txt" {
		t.Fatalf("fetched %v", back.fetched)
	}
	if !strings.Contains(m.View(), "/somewhere/drop/notes.txt") {
		t.Errorf("it did not say where the file landed:\n%s", m.View())
	}
}

// What may be done to a directory is what the far end said, not what this end would like.
func TestAReadOnlyDirectoryOffersNothingThatChangesIt(t *testing.T) {
	m := walkTo(t, withFiles(false), "/work")

	shown := m.View()
	if !strings.Contains(shown, "read only") {
		t.Errorf("a read-only directory did not say so:\n%s", shown)
	}
	for _, gone := range []string{"remove", "send"} {
		if strings.Contains(shown, gone) {
			t.Errorf("a read-only directory offered %q:\n%s", gone, shown)
		}
	}

	m.list.Select(1)
	if m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); m.removing != "" {
		t.Error("x asked about removing something from a read-only directory")
	}
	if m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}); m.putting {
		t.Error("s offered to send into a read-only directory")
	}
}

// A writable one takes something back, at whichever directory the level is standing in.
func TestSendingIntoADirectoryGoesWhereYouAreStanding(t *testing.T) {
	back := withFiles(true)
	m := walkTo(t, back, "/work")

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.putting {
		t.Fatal("s did not ask for a name")
	}
	if !strings.Contains(m.View(), "send") {
		t.Errorf("the line to type on was not drawn:\n%s", m.View())
	}

	file := writeOne(t, "sent.txt")
	for _, r := range file {
		m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(back.uploaded) != 1 || back.uploaded[0] != "/work/reports/sent.txt" {
		t.Fatalf("uploaded %v", back.uploaded)
	}
}

// Your own directory is walked the same way somebody else's is.
func TestYourOwnDirectoryIsWalkedTheSameWay(t *testing.T) {
	when := time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)
	back := &fake{
		mine: []proto.Served{{Path: "/work", Archetype: "files", Writable: true}},
		holding: map[string][]Held{
			"/work":         {{Name: "reports", Dir: true, At: when}},
			"/work/reports": {{Name: "july.pdf", Size: 1024, At: when}},
		},
	}

	m := settle(t, intoSelf(t, start(t, back)), tea.KeyMsg{Type: tea.KeyEnter})
	if m.at != levelBrowse {
		t.Fatalf("your own files namespace went to level %d", m.at)
	}

	m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.dir != "reports" || !strings.Contains(m.View(), "july.pdf") {
		t.Fatalf("walking your own directory stood at %q:\n%s", m.dir, m.View())
	}

	// Nothing is downloaded from this machine to this machine, and nothing is asked of a peer.
	if m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")}); len(back.fetched) != 0 {
		t.Errorf("it fetched from this machine: %v", back.fetched)
	}
	if len(back.listed) != 0 {
		t.Errorf("it asked a peer about this machine's own directory: %v", back.listed)
	}
}

// writeOne puts a file on this disk to be sent, and hands back its whole path.
func writeOne(t *testing.T, name string) string {
	t.Helper()

	at := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(at, []byte("what is in it"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", at, err)
	}
	return at
}

// A directory that cannot be read says so, rather than looking empty.
func TestADirectoryThatCannotBeReadSaysSo(t *testing.T) {
	back := withFiles(false)
	back.refuseWalk = errors.New("beta did not answer")

	m := walkTo(t, back, "/work")
	if !strings.Contains(m.View(), "beta did not answer") {
		t.Errorf("a listing that failed was drawn as an empty directory:\n%s", m.View())
	}
}
