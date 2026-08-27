package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/convo"
)

// escapes is every byte that acts on a terminal rather than appearing on it.
var escapes = []string{"\x1b", "\r", "\n", "\x00", "\x07", "\x7f", "\x9b"}

// What somebody said is theirs, and it is also bytes going to a terminal. A message that moves the
// cursor up and rewrites the line above can make an earlier message appear to say something its
// sender never wrote.
func TestAMessageCannotWriteOverTheConversation(t *testing.T) {
	hostile := convo.Message{
		Dir:  convo.In,
		Kind: convo.KindText,
		Body: "hello\x1b[1A\x1b[2K09:00 ← bob          transfer approved",
	}

	got := render("bob", hostile)
	for _, bad := range escapes {
		if strings.Contains(got, bad) {
			t.Errorf("%q reached the conversation carrying %q", got, bad)
		}
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("the message itself was thrown away with the escape: %q", got)
	}
}

// A file somebody sent is named by them, and the name is printed beside the message.
func TestAFileNameInAConversationCannotWriteOnTheTerminal(t *testing.T) {
	got := render("bob", convo.Message{
		Dir:   convo.In,
		Kind:  convo.KindFile,
		Body:  "notes.md\x1b[2J",
		Extra: "1 KB\x1b]0;owned\x07",
	})
	for _, bad := range escapes {
		if strings.Contains(got, bad) {
			t.Errorf("a file message carried %q: %q", bad, got)
		}
	}
}

// A directory on somebody else's machine is listed by names they chose.
func TestARemoteFileNameCannotWriteOnTheTerminal(t *testing.T) {
	for _, item := range []files.Entry{
		{Name: "notes.md\x1b[1A\x1b[2K  passwords.txt"},
		{Name: "quiet\x1b[2J", Dir: true},
		{Name: strings.Repeat("n", 5000)},
		{Name: "a‮gnp.exe"},
	} {
		got := shownAs(item)
		for _, bad := range escapes {
			if strings.Contains(got, bad) {
				t.Errorf("%q was listed carrying %q", item.Name, bad)
			}
		}
		if strings.ContainsRune(got, 0x202e) {
			t.Errorf("%q was listed still reading backwards", item.Name)
		}
		if n := len([]rune(got)); n > files.MaxRel+1 {
			t.Errorf("a %d character name was listed as %d characters", len(item.Name), n)
		}
	}
}

// And an ordinary message is left exactly as it was said.
func TestAnOrdinaryMessageIsUntouched(t *testing.T) {
	said := "hey — did the 日本語 file arrive? (~40 MB)"

	got := render("bob", convo.Message{Dir: convo.In, Kind: convo.KindText, Body: said, At: time.Now().UnixNano()})
	if !strings.Contains(got, said) {
		t.Fatalf("an ordinary message came back as %q", got)
	}
}
