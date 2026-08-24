package tui

import (
	"context"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/proto"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(m.listWidth(), m.listHeight())
		if m.screen != nil {
			m.screen.Resize(m.viewWidth(), m.viewHeight())
		}
		return m, nil

	case tea.MouseMsg:
		// The wheel scrolls the conversation. Everywhere else the list widget has its own idea of
		// what a wheel means, and it is a better one than anything invented here.
		if !m.reading() {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}

		switch msg.Button {
		case tea.MouseButtonWheelUp:
			return m.scrollBy(3), nil
		case tea.MouseButtonWheelDown:
			return m.scrollBy(-3), nil
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)

	case ruleLoaded:
		m.loading = false
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		m.rule, m.trouble = msg.rule, ""
		m.showAccess()
		return m, nil

	case changed:
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		// Read back rather than believed: what is on screen has to be what a caller will be
		// judged against, and that is on disk.
		return m, loadRule(m.back, msg.path)

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

	case arrived:
		// Only what is being looked at is asked for again. Rebuilding the device list while
		// somebody is reading a conversation moves the ground under them for no reason.
		next := []tea.Cmd{listenFor(m.back.Arrivals())}

		switch {
		case m.at == levelDevices:
			next = append(next, loadPeers(m.back))
		case m.at == levelOpen:
			if with, ok := m.peer(); ok {
				next = append(next, loadHistory(m.back, with))
			}
		}
		return m, tea.Batch(next...)

	case tick:
		if m.offering == nil {
			return m, nil
		}
		return m, ticking()

	case putDone:
		m.offering = nil
		if msg.err != nil {
			m.trouble, m.said = msg.err.Error(), ""
			return m, nil
		}
		m.trouble, m.said = "", "sent "+msg.what
		if at, ok := m.peer(); ok {
			return m, loadHistory(m.back, at)
		}
		return m, nil

	case joined:
		m.loading = false
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
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
		m.peers, m.reaching, m.trouble = msg.peers, msg.reaching, ""
		if m.at == levelDevices {
			m.showDevices()
		}
		return m, nil

	case pathsLoaded:
		m.loading = false

		// An answer is for whichever device is open. This device answers under no name, because it
		// is not in the address book and does not need to be.
		if m.onSelf {
			if msg.peer != "" {
				return m, nil
			}
		} else if at, ok := m.peer(); !ok || at.Name != msg.peer {
			return m, nil // a stale answer for a device we have moved off
		}
		if msg.err != nil {
			// What it last said is still the best guess at what it shares. Emptying the list
			// because one answer went missing throws away the only thing worth showing.
			m.trouble = msg.err.Error()

			// A list may come back with the failure: what the device said the last time anybody
			// asked. It is worth showing, because a conversation with a device that is off is
			// still on this disk and there is no other way in to it.
			if len(msg.paths) == 0 {
				return m, nil
			}
			m.trouble = "not reachable — showing what it last shared"
		}

		if m.known == nil {
			m.known = map[string][]proto.Served{}
		}
		m.known[msg.peer] = msg.paths

		m.paths = msg.paths
		if msg.err == nil {
			m.trouble = ""
		}
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
		// The conversation loading says nothing about whatever else went wrong. Clearing the
		// notice here wipes the one from the message that has not gone out yet.
		// Somebody reading back stays where they were. The window counts lines from the newest, so
		// without this every arriving message drags what is being read one message further away.
		if m.scroll > 0 {
			was := len(m.chatLines())
			m.history, m.waiting = msg.log, msg.waiting
			m.scroll += len(m.chatLines()) - was
			return m, nil
		}

		m.history, m.waiting = msg.log, msg.waiting
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
		// Written down. On screen straight away, and sent underneath: what somebody typed should
		// appear at the speed of a disk, not at the speed of the far end's network.
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		m.trouble = ""

		if at, ok := m.peer(); ok {
			return m, tea.Batch(loadHistory(m.back, at), deliver(m.back, at))
		}
		return m, nil

	case delivered:
		// A failure is worth saying once, but it is not the message being lost: it stays in the
		// conversation, marked as still on its way, and goes out when the far end appears.
		m.trouble = ""
		if msg.err != nil {
			m.trouble = "still on its way: " + msg.err.Error()
		}

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

	// A ticket being typed owns the keyboard the same way: it is one field and two ways out.
	if m.joining {
		return m.joinKey(msg)
	}

	if m.putting {
		return m.putKey(msg)
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

	// Reading back through a conversation, where the arrows are the conversation's rather than a
	// list's. Everything else on this screen is one key.
	if m.reading() {
		switch msg.String() {
		case "up", "k":
			return m.scrollBy(1), nil
		case "down", "j":
			return m.scrollBy(-1), nil
		case "pgup":
			return m.scrollBy(m.viewHeight() - 2), nil
		case "pgdown":
			return m.scrollBy(-(m.viewHeight() - 2)), nil
		case "home":
			return m.scrollBy(len(m.chatLines())), nil
		case "end":
			return m.scrollBy(-len(m.chatLines())), nil
		}
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

	case "s":
		// One key for both, because they are the same act: name a thing and send it there.
		if at, ok := m.path(); ok && m.at == levelOpen && putsInto(kindOf(at)) {
			m.putting, m.typing, m.options, m.said, m.trouble = true, "", nil, "", ""
		}
		return m, nil

	case "p":
		if m.at == levelDevices && m.linking == nil {
			return m, offer(m.back)
		}
		return m, nil

	case "t":
		if m.at == levelDevices && m.linking == nil {
			m.joining, m.typing, m.trouble = true, "", ""
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

	case "w":
		// Who may reach it. Only for your own paths: what somebody else shares and with whom is
		// their business, and nothing here could change it anyway.
		if at, ok := m.path(); ok && m.at == levelPaths && m.onSelf {
			m.at, m.loading, m.trouble = levelAccess, true, ""
			m.rule = Rule{Path: at.Path}
			m.showAccess()
			return m, loadRule(m.back, at.Path)
		}
		return m, nil

	case "a", "x", "d":
		if m.at != levelAccess {
			return m, nil
		}
		it, ok := m.standingOf()
		if !ok {
			return m, nil
		}

		to := Allowed
		switch msg.String() {
		case "x":
			to = Refused
		case "d":
			to = NotNamed
		}
		return m, change(m.back, m.rule.Path, it.who.Name, to)
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
		// A divider is a label and a person is a heading. Neither is a thing to enter: what you
		// reach is a machine, and a person's machines are the rows underneath their name.
		switch m.list.SelectedItem().(type) {
		case dividerItem, personItem:
			return m, nil
		}

		// This machine's own row. Everything a peer's list does, it does — except that what it
		// shares is read from this machine's own config instead of asked for over a wire.
		if m.onSelfRow() {
			m.onSelf = true
			m.at = levelPaths
			m.paths, m.loading = nil, true
			m.atPath, m.under = 0, "/"
			m.showPaths()

			return m, loadMine(m.back)
		}

		if len(m.peers) == 0 {
			return m, nil
		}
		m.onSelf = false
		m.atPeer = m.peerFor(m.list.Index())
		m.at = levelPaths

		with, _ := m.peer()

		// What it said last time, straight away, and the question asked underneath. A device that
		// has not been visited shows nothing and says it is asking.
		m.paths = m.known[with.Name]
		m.loading = len(m.paths) == 0
		m.atPath, m.under = 0, "/"
		m.showPaths()

		return m, loadPaths(m.back, with)

	case levelPaths:
		if len(m.steps) == 0 {
			return m, nil
		}

		at := m.steps[m.list.Index()]

		// A way down is walked into, whether or not it is also a namespace: what is inside is
		// listed along with the path itself, so nothing becomes unreachable by having something
		// underneath it.
		if at.below > 0 && !at.here {
			m.under, m.atPath = at.at, 0
			m.showPaths()
			return m, nil
		}
		if !at.is {
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
	case levelAccess:
		m.at, m.trouble = levelPaths, ""
		m.showPaths()
		return m, nil

	case levelOpen:
		m.stop()
		m.history = nil
		m.at = levelPaths
		m.showPaths()
		return m, nil

	case levelPaths:
		// Out of a folder before out of the device: walking in three levels and being thrown all
		// the way out is not what going back means anywhere else.
		if m.under != "/" && m.under != "" {
			m.under, m.atPath = up(m.under), 0
			m.showPaths()
			return m, nil
		}

		m.at, m.onSelf = levelDevices, false
		m.paths, m.trouble = nil, ""
		m.showDevices()
		return m, nil
	}
	return m, nil
}

// showDevices puts the address book in the list, grouped.
func (m *Model) showDevices() {
	m.rows = group(m.me, m.peers, m.reaching)
	m.list.SetItems(m.rows.items)
	m.list.Select(m.rowFor(m.atPeer))
	m.list.SetSize(m.listWidth(), m.listHeight())
}

// showPaths puts what the open device shares in the list.
func (m *Model) showPaths() {
	with, _ := m.peer()
	if m.onSelf {
		with = book.Entry{Name: m.me.Name}
	}

	m.steps = walk(m.paths, m.under)

	items := make([]list.Item, 0, len(m.steps))
	for _, at := range m.steps {
		items = append(items, pathItem{step: at, on: with.Name})
	}
	m.list.SetItems(items)
	m.list.Select(m.atPath)
	m.list.SetSize(m.listWidth(), m.listHeight())
}

// openPath does whatever the path is: reads a conversation, or starts watching.
func (m *Model) openPath() tea.Cmd {
	m.stop()
	m.history = nil

	// A new screen starts at the newest, without the last one's complaint on it.
	m.trouble, m.said, m.scroll = "", "", 0

	at, okPath := m.path()
	with, okPeer := m.peer()
	if !okPath || !okPeer {
		return nil
	}

	switch kindOf(at) {
	case "chat", "files", "link", "bookmark":
		// All three read the same conversation: what was said, what changed hands, and what was
		// opened are one record, and a files path is a view onto part of it.
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

// joinKey takes a ticket a character at a time.
//
// A ticket arrives pasted more often than typed, and a paste reaches a terminal program as one
// run of runes, which is why nothing here works a key at a time except the corrections.
func (m Model) joinKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.joining, m.typing = false, ""
		return m, nil

	case tea.KeyEnter:
		ticket := strings.TrimSpace(m.typing)
		if ticket == "" {
			return m, nil
		}
		m.joining, m.typing, m.loading = false, "", true
		return m, join(m.back, ticket)

	case tea.KeyBackspace:
		if n := len(m.typing); n > 0 {
			m.typing = m.typing[:n-1]
		}
		return m, nil

	case tea.KeyRunes:
		m.typing += string(msg.Runes)
		return m, nil

	case tea.KeySpace:
		return m, nil
	}
	return m, nil
}

// putKey takes a file path or a URL, one key at a time.
func (m Model) putKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.putting, m.typing, m.options = false, "", nil
		return m, nil

	case tea.KeyTab:
		// Completion is for paths. A URL has nothing on this machine to complete against.
		at, ok := m.path()
		if !ok || kindOf(at) != "files" {
			return m, nil
		}

		finished, options := complete(m.typing)
		m.typing, m.options = finished, options
		if len(options) == 1 {
			m.options = nil
		}
		return m, nil

	case tea.KeyEnter:
		body := strings.TrimSpace(m.typing)
		if body == "" {
			return m, nil
		}

		at, ok := m.path()
		if !ok {
			return m, nil
		}
		with, ok := m.peer()
		if !ok {
			return m, nil
		}

		m.putting, m.typing, m.options, m.trouble, m.said = false, "", nil, "", ""

		if kindOf(at) == "files" {
			file := expand(body)
			if _, err := os.Stat(file); err != nil {
				m.trouble = "no such file: " + file
				return m, nil
			}
			m.offering = &moving{}
			return m, tea.Batch(putFile(m.back, with, at.Path, file, m.offering), ticking())
		}
		return m, putLink(m.back, with, at.Path, body)

	case tea.KeyBackspace:
		if n := len(m.typing); n > 0 {
			m.typing = m.typing[:n-1]
			m.options = nil
		}
		return m, nil

	case tea.KeyRunes:
		m.typing += string(msg.Runes)
		m.options = nil
		return m, nil

	case tea.KeySpace:
		m.typing += " "
		m.options = nil
		return m, nil
	}
	return m, nil
}

// putsInto reports whether a path is somewhere you can send something.
func putsInto(kind string) bool {
	return kind == "files" || kind == "link" || kind == "bookmark"
}

// reading reports whether a conversation is open and being read rather than written.
func (m Model) reading() bool {
	if m.at != levelOpen || m.writing || m.putting {
		return false
	}

	at, ok := m.path()
	return ok && kindOf(at) == "chat"
}

// scrollBy moves back through the conversation, or forward again. Positive is towards older.
func (m Model) scrollBy(lines int) Model {
	room := m.viewHeight() - 3

	most := len(m.chatLines()) - room
	if most < 0 {
		most = 0
	}

	m.scroll += lines
	if m.scroll > most {
		m.scroll = most
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	return m
}

// onSelfRow reports whether the cursor is on this machine.
func (m Model) onSelfRow() bool { return m.list.Index() == m.rows.me }

// rowFor is where a peer sits in the list.
//
// Looked up rather than counted: the list is grouped, so how many labels sit above a device
// depends on who is in the address book.
func (m Model) rowFor(peer int) int {
	if peer < 0 || peer >= len(m.rows.row) || m.rows.row[peer] < 0 {
		return m.rows.me
	}
	return m.rows.row[peer]
}

// peerFor is which peer a row is, and the one already open for a row that is not a device.
func (m Model) peerFor(row int) int {
	if at, ok := m.rows.peer[row]; ok {
		return at
	}
	return m.atPeer
}
