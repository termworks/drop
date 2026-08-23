package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(m.width, m.listHeight())
		if m.screen != nil {
			m.screen.Resize(m.viewWidth(), m.viewHeight())
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)

	case pairStarted:
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		m.linking, m.trouble = msg.at, ""
		return m, waitForPair(msg.at.waited)

	case pairDone:
		if m.linking != nil {
			m.linking.stop()
			m.linking = nil
		}
		m.trouble = ""
		return m, loadPeers(m.back)

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
		if m.at == levelDevices {
			m.showDevices()
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
		m.paths, m.trouble = msg.paths, ""
		if m.at == levelPaths {
			m.showPaths()
		}
		return m, nil

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
	// While a pairing is on screen it owns the keyboard: there is one thing to do, and one way
	// out of it.
	if m.linking != nil {
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			m.linking.stop()
			m.linking = nil
		}
		return m, nil
	}

	if m.writing {
		return m.typeKey(msg)
	}

	// While filtering, the list owns the keyboard.
	if m.at != levelOpen && m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		m.stop()
		return m, tea.Quit

	case "q":
		// q leaves the level rather than the program, until there is nowhere left to go back to.
		if m.at == levelDevices {
			m.stop()
			return m, tea.Quit
		}
		return m.back_()

	case "esc", "left", "h":
		return m.back_()

	case "enter", "right", "l":
		return m.enter()

	case "i":
		if at, ok := m.path(); ok && m.at == levelOpen && kindOf(at) == "chat" {
			m.writing = true
		}
		return m, nil

	case "p":
		if m.at == levelDevices && m.linking == nil {
			return m, offer(m.back)
		}
		return m, nil

	case "r":
		m.loading = true
		if m.at == levelPaths {
			if with, ok := m.peer(); ok {
				return m, loadPaths(m.back, with)
			}
		}
		return m, loadPeers(m.back)
	}

	if m.at != levelOpen {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) typeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	case tea.KeyRunes:
		m.typing += string(msg.Runes)
		return m, nil

	case tea.KeySpace:
		m.typing += " "
		return m, nil
	}
	return m, nil
}

// enter goes one level deeper: a device, then a path, then the thing itself.
func (m Model) enter() (tea.Model, tea.Cmd) {
	switch m.at {
	case levelDevices:
		if len(m.peers) == 0 {
			return m, nil
		}
		m.atPeer = m.list.Index()
		m.at = levelPaths
		m.paths = nil
		m.loading = true
		m.showPaths()

		with, _ := m.peer()
		return m, loadPaths(m.back, with)

	case levelPaths:
		if len(m.paths) == 0 {
			return m, nil
		}
		m.atPath = m.list.Index()
		m.at = levelOpen
		return m, m.openPath()
	}
	return m, nil
}

// back_ comes out one level, stopping whatever the level was doing.
func (m Model) back_() (tea.Model, tea.Cmd) {
	switch m.at {
	case levelOpen:
		m.stop()
		m.history = nil
		m.at = levelPaths
		m.showPaths()
		return m, nil

	case levelPaths:
		m.at = levelDevices
		m.paths, m.trouble = nil, ""
		m.showDevices()
		return m, nil
	}
	return m, nil
}

// showDevices puts the address book in the list.
func (m *Model) showDevices() {
	items := make([]list.Item, 0, len(m.peers))
	for _, p := range m.peers {
		items = append(items, deviceItem{entry: p, addr: strings.Join(p.Addrs, "  ")})
	}
	m.list.SetItems(items)
	m.list.Select(m.atPeer)
	m.list.SetSize(m.width, m.listHeight())
}

// showPaths puts what the open device shares in the list.
func (m *Model) showPaths() {
	with, _ := m.peer()

	items := make([]list.Item, 0, len(m.paths))
	for _, s := range m.paths {
		items = append(items, pathItem{served: s, on: with.Name})
	}
	m.list.SetItems(items)
	m.list.Select(m.atPath)
	m.list.SetSize(m.width, m.listHeight())
}

// openPath does whatever the path is: reads a conversation, or starts watching.
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
