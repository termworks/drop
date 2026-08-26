package note

import (
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/made"
)

func TestANoteNeedsAFileAndSaysSo(t *testing.T) {
	n := New(Into{})

	if _, err := n.Read(made.Declared(made.Settings{})); err == nil {
		t.Fatal("a note with no file was read")
	} else if !strings.Contains(err.Error(), "file") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}

	cfg, err := n.Read(made.Declared(made.Settings{"file": "/tmp/notes.md"}))
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if got := cfg.(Config).File; got != "/tmp/notes.md" {
		t.Errorf("the file is %q", got)
	}
	if note := n.Note(cfg); !note.Shareable || note.Detail != "/tmp/notes.md" {
		t.Errorf("a note says %+v about itself", note)
	}
}

// Whether several machines may hold one is asked before a declaration has been read, so it cannot
// depend on one.
func TestANoteIsSomethingSeveralMachinesHoldBeforeAnybodyDeclaresOne(t *testing.T) {
	if !New(Into{}).Note(nil).Shareable {
		t.Fatal("a note with nothing declared says it is one machine's own")
	}
}
