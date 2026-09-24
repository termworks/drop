package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Every action a screen has, one press away.
//
// The keys are the same list the line along the bottom shows the start of and ? shows whole; space
// lays them out as a menu to pick from, and picking one presses its key. So an action is never
// only reachable by knowing which letter it is.

// menuState is the menu while it is open, and which action the cursor is on.
type menuState struct {
	items []hint
	at    int
	// title and pick make it a choice rather than a list of keys: what is picked is handed to
	// pick instead of being pressed.
	title string
	pick  func(key string) tea.Cmd
	// ids, when set, is what each item hands pick in place of its key.
	ids []string
}

// acting is what the menu offers: the keys that do something, not the ones that move about.
func (m Model) acting() []hint {
	// Whatever has the keyboard — a line being typed, a question, a code on screen — is the one
	// thing to do, and space is part of it.
	if m.prompt != nil || m.confirm != nil || m.menu != nil || m.writing || m.joining || m.putting ||
		m.linking != nil || m.removing != "" || m.atKeyboard || m.helping {
		return nil
	}
	var out []hint
	for _, k := range m.keys() {
		switch k.key {
		case "↑↓", "esc", "q", "?", "space":
			continue
		}
		out = append(out, k)
	}
	return out
}

// openMenu lays the screen's actions out to pick from.
func (m Model) openMenu() (tea.Model, tea.Cmd) {
	items := m.acting()
	if len(items) == 0 {
		return m, nil
	}
	m.menu = &menuState{items: items}
	return m, nil
}

// menuKey moves through the menu, and presses whichever key was picked.
func (m Model) menuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", " ", "ctrl+c":
		m.menu = nil
		return m, nil
	case "up", "k":
		if m.menu.at > 0 {
			m.menu.at--
		}
		return m, nil
	case "down", "j":
		if m.menu.at < len(m.menu.items)-1 {
			m.menu.at++
		}
		return m, nil
	case "enter":
		picked, pick := m.menu.items[m.menu.at].key, m.menu.pick
		if m.menu.ids != nil {
			picked = m.menu.ids[m.menu.at]
		}
		m.menu = nil
		if pick != nil {
			return m, pick(picked)
		}
		return m.key(pressOf(picked))
	}
	if m.menu.pick != nil {
		return m, nil
	}
	// A key pressed with the menu open is that action, the same as without it.
	for _, it := range m.menu.items {
		if it.key == msg.String() {
			m.menu = nil
			return m.key(msg)
		}
	}
	return m, nil
}

// pressOf is a key as the keyboard would have sent it.
func pressOf(key string) tea.KeyMsg {
	if key == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

// menuView is the menu, in a panel of its own.
func (m Model) menuView() string {
	var out strings.Builder
	out.WriteString("\n")
	width, title, does := 7, "what you can do here", "enter does it"
	if m.menu.pick != nil {
		width, title, does = 9, m.menu.title, "enter picks it"
	}
	for i, it := range m.menu.items {
		line := " " + keyStyle.Render(fitted(it.key, width)) + "  " + faintStyle.Render(it.does)
		if i == m.menu.at {
			line = " " + keyStyle.Render(fitted(it.key, width)) + "  " + brandStyle.Render("› "+it.does)
		}
		out.WriteString(line + "\n")
	}
	out.WriteString("\n " + faintStyle.Render("↑↓ pick · "+does+" · esc closes"))
	return m.middle(panel(title, m.panelWidth(), 0, out.String()))
}

// footer is the line along the bottom: as many of the screen's keys as fit, and where the rest are.
func (m Model) footer() string {
	keys := m.keys()
	more := keyStyle.Render("space") + " " + dimStyle.Render("all actions") + faintStyle.Render(" · ") + keyStyle.Render("?") + " " + dimStyle.Render("keys")
	if len(m.acting()) == 0 {
		more = faintStyle.Render("? keys")
	}
	room := m.width - 2 - lipgloss.Width(more) - 4

	var parts []string
	used := 0
	for _, k := range keys {
		part := keyStyle.Render(k.key) + " " + dimStyle.Render(k.does)
		w := lipgloss.Width(part) + 3
		if used+w > room {
			break
		}
		parts = append(parts, part)
		used += w
	}
	line := strings.Join(parts, faintStyle.Render(" · "))
	if line != "" {
		line += faintStyle.Render(" · ")
	}
	return " " + line + more
}
