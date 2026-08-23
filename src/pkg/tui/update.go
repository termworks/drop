package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.screen != nil {
			m.screen.Resize(m.viewWidth(), m.viewHeight())
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)

	case selfLoaded:
		if msg.err == nil {
			m.me = msg.me
		}
		return m, nil

	case peersLoaded:
		m.loading = false
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		m.peers, m.trouble = msg.peers, ""
		if len(m.peers) > 0 {
			return m, m.openPeer()
		}
		return m, nil

	case pathsLoaded:
		m.loading = false
		if at, ok := m.peer(); !ok || at.Name != msg.peer {
			return m, nil // a stale answer for a device we have moved off
		}
		if msg.err != nil {
			m.paths, m.trouble = nil, msg.err.Error()
			return m, nil
		}
		m.paths, m.atPath, m.trouble = msg.paths, 0, ""
		return m, m.openPath()

	case historyLoaded:
		if at, ok := m.peer(); !ok || at.Name != msg.peer {
			return m, nil
		}
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		m.history, m.trouble = msg.log, ""
		return m, nil

	case framePainted:
		if m.screen == nil {
			return m, nil
		}
		return m, waitForFrame(m.screen.nudge)

	case watchEnded:
		m.live = false
		if msg.err != nil && !strings.Contains(msg.err.Error(), "context canceled") {
			m.trouble = msg.err.Error()
		}
		return m, nil

	case saidIt:
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		m.trouble = ""
		if at, ok := m.peer(); ok {
			return m, loadHistory(m.back, at)
		}
		return m, nil
	}
	return m, nil
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While composing, the keys are the message — except the two that end it.
	if m.writing {
		switch msg.Type {
		case tea.KeyEsc:
			m.writing, m.typing = false, ""
			return m, nil
		case tea.KeyEnter:
			body := strings.TrimSpace(m.typing)
			m.typing = ""
			if body == "" {
				return m, nil
			}
			at, ok := m.peer()
			if !ok {
				return m, nil
			}
			return m, say(m.back, at, body)
		case tea.KeyBackspace:
			if n := len(m.typing); n > 0 {
				m.typing = m.typing[:n-1]
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.typing += string(msg.Runes)
			if msg.Type == tea.KeySpace {
				m.typing += " "
			}
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.stop()
		return m, tea.Quit

	case "tab":
		m.focus = (m.focus + 1) % 3
		return m, nil
	case "shift+tab":
		m.focus = (m.focus + 2) % 3
		return m, nil

	case "left", "h":
		if m.focus > panePeers {
			m.focus--
		}
		return m, nil
	case "right", "l":
		if m.focus < paneView {
			m.focus++
		}
		return m, nil

	case "up", "k":
		return m.move(-1)
	case "down", "j":
		return m.move(1)

	case "enter":
		if m.focus == panePeers {
			m.focus = panePaths
			return m, nil
		}
		if m.focus == panePaths {
			m.focus = paneView
			return m, nil
		}
		return m, nil

	case "i":
		if at, ok := m.path(); ok && kindOf(at) == "chat" {
			m.writing, m.focus = true, paneView
		}
		return m, nil

	case "r":
		m.loading = true
		return m, loadPeers(m.back)
	}
	return m, nil
}

// move steps the list the focused pane is showing.
func (m Model) move(by int) (tea.Model, tea.Cmd) {
	switch m.focus {
	case panePeers:
		next := m.atPeer + by
		if next < 0 || next >= len(m.peers) {
			return m, nil
		}
		m.atPeer = next
		return m, m.openPeer()

	case panePaths:
		next := m.atPath + by
		if next < 0 || next >= len(m.paths) {
			return m, nil
		}
		m.atPath = next
		return m, m.openPath()
	}
	return m, nil
}

// openPeer asks the newly selected device what it shares.
func (m *Model) openPeer() tea.Cmd {
	m.stop()
	m.paths, m.atPath, m.history = nil, 0, nil

	at, ok := m.peer()
	if !ok {
		return nil
	}
	m.loading = true
	return loadPaths(m.back, at)
}

// openPath does whatever the path at the cursor is: reads a conversation, or starts watching.
func (m *Model) openPath() tea.Cmd {
	m.stop()
	m.history = nil

	at, okPath := m.path()
	with, okPeer := m.peer()
	if !okPath || !okPeer {
		return nil
	}

	switch kindOf(at) {
	case "chat":
		return loadHistory(m.back, with)

	case "tty", "stream":
		m.screen = newScreen(m.viewWidth(), m.viewHeight())
		m.live = true

		ctx, stop := context.WithCancel(context.Background())
		m.stopped = stop

		return tea.Batch(
			watch(m.back, with, at.Path, m.screen, ctx),
			waitForFrame(m.screen.nudge),
		)
	}
	return nil
}
