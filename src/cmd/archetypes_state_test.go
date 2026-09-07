package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/convo"
)

func TestLandingReportsConversationFailure(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", blocked)

	var notes, trouble []string
	doing := &doings{
		notes:   func(text string) { notes = append(notes, text) },
		trouble: func(text string) { trouble = append(trouble, text) },
	}
	doing.landed(idFor(64), "report.pdf", 12)

	for _, note := range trouble {
		if strings.Contains(note, "recording") && strings.Contains(note, "report.pdf") {
			if len(notes) != 1 || !strings.Contains(notes[0], "received report.pdf") {
				t.Fatalf("ordinary transfer notes = %v", notes)
			}
			return
		}
	}
	t.Fatalf("conversation failure was silent: %v", trouble)
}

func TestFileTransferIsRecorded(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	peer := idFor(65)

	if err := noteFile(peer, convo.Out, "report.pdf", 12); err != nil {
		t.Fatal(err)
	}
	store, err := convo.Open(peer)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Kind != convo.KindFile || history[0].Dir != convo.Out || history[0].Body != "report.pdf" || history[0].Extra != "12 B" {
		t.Fatalf("recorded transfer = %+v", history)
	}
}
