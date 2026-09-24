package tui

import (
	"fmt"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// Seen wraps an interface so what it last drew can be read from outside it: something driving the
// interface from another terminal has to be able to see what it is driving.
func Seen(m tea.Model) (tea.Model, *Screen) {
	shown := &Screen{}
	return seen{inner: m, shown: shown}, shown
}

// Screen is what an interface last drew.
type Screen struct {
	mu   sync.Mutex
	last string
}

// Now is the screen as it stands, escapes and all.
func (s *Screen) Now() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

type seen struct {
	inner tea.Model
	shown *Screen
}

func (s seen) Init() tea.Cmd { return s.inner.Init() }

func (s seen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := s.inner.Update(msg)
	return seen{inner: next, shown: s.shown}, cmd
}

func (s seen) View() string {
	drawn := s.inner.View()
	s.shown.mu.Lock()
	s.shown.last = drawn
	s.shown.mu.Unlock()
	return drawn
}

// named is every key the interface knows by name, the way it matches on them: "enter", "esc",
// "ctrl+]", "down". Built from the terminal library's own names rather than a copy of them.
var named = func() map[string]tea.KeyType {
	out := map[string]tea.KeyType{"space": tea.KeySpace}
	for k := tea.KeyType(-128); k < 128; k++ {
		if name := k.String(); name != "" && name != "runes" && name != " " {
			out[name] = k
		}
	}
	return out
}()

// Key is one key by name — "enter", "esc", "ctrl+]", "alt+x" — or a single character, which is
// that character typed.
func Key(name string) (tea.KeyMsg, error) {
	alt := false
	if rest, found := strings.CutPrefix(name, "alt+"); found && rest != "" {
		alt, name = true, rest
	}
	if k, ok := named[name]; ok {
		return tea.KeyMsg{Type: k, Alt: alt}, nil
	}
	if runes := []rune(name); len(runes) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes, Alt: alt}, nil
	}
	return tea.KeyMsg{}, fmt.Errorf("%q is not a key: a name like enter, esc, down or ctrl+], or one character", name)
}

// Typed is text as the keys that type it, one after another.
func Typed(text string) []tea.KeyMsg {
	out := make([]tea.KeyMsg, 0, len(text))
	for _, r := range text {
		if r == ' ' {
			out = append(out, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			continue
		}
		out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return out
}
