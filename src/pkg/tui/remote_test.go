package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A key is named the way the interface matches on it, so what is pressed from outside is what a
// person pressing it would have sent.
func TestKeysAreNamedTheWayTheInterfaceMatchesThem(t *testing.T) {
	for _, name := range []string{"enter", "esc", "down", "tab", "ctrl+]", "ctrl+c", "backspace", "p", "/", "?"} {
		k, err := Key(name)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if k.String() != name {
			t.Fatalf("%q came out as %q", name, k.String())
		}
	}

	if k, err := Key("alt+x"); err != nil || k.String() != "alt+x" {
		t.Fatalf("alt+x came out as %q, %v", k.String(), err)
	}
	if k, err := Key("space"); err != nil || k.Type != tea.KeySpace {
		t.Fatalf("space came out as %v, %v", k, err)
	}
	if _, err := Key("enterr"); err == nil {
		t.Fatal("a misspelt key was taken")
	}
}

// Typed text is the keys that type it, spaces included, so a ticket or a message arrives whole.
func TestTextIsTypedAKeyAtATime(t *testing.T) {
	var out string
	for _, k := range Typed("hi there") {
		out += k.String()
	}
	if out != "hi there" {
		t.Fatalf("typed %q", out)
	}
}

// What was drawn last is what is shown, however many times it has been drawn since.
func TestSeenRemembersTheLastScreen(t *testing.T) {
	m, shown := Seen(New(&fake{}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	drawn := m.View()

	if shown.Now() != drawn {
		t.Fatal("the screen remembered is not the one drawn")
	}
}
