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

			// The far end draws for the window it is being watched in, whether or not it takes
			// keys. Its shape is not something it has to be trusted with.
			if m.typingAt != nil {
				_ = m.typingAt.Resize(m.viewWidth(), m.viewHeight())
			}
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

	case managing:
		m.loading = false
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		m.managed, m.trouble = msg.who, ""
		if m.at == levelManage {
			m.showManage()
		}
		return m, nil

	case managed:
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		if msg.gone {
			m.at, m.trouble = levelUsers, ""
			return m, loadPeers(m.back)
		}
		// Read back rather than believed, the same as a grant.
		return m, loadManaged(m.back, msg.name)

	case rang:
		m.said = ""
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		m.trouble, m.said = "", "asked for "+msg.path+" — somebody there decides"
		return m, nil

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
		case m.at == levelUsers:
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
		if m.at == levelBrowse {
			m.loading = true
			return m, m.listing()
		}
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
		m.peers, m.reaching, m.knocked, m.trouble = msg.peers, msg.reaching, msg.knocked, ""
		if m.at == levelUsers {
			m.showUsers()
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

	case heldLoaded:
		m.loading = false

		// The directory as well as the path: an answer for one directory of a namespace says
		// nothing about the one that is on screen by the time it arrives.
		at, ok := m.path()
		if !ok || m.at != levelBrowse || at.Path != msg.path || m.dir != msg.dir {
			return m, nil
		}
		if msg.err != nil {
			m.trouble = msg.err.Error()
			return m, nil
		}
		m.held, m.trouble = msg.held, ""
		m.showBrowse()
		return m, nil

	case fetched:
		m.offering = nil
		if msg.err != nil {
			m.trouble, m.said = msg.err.Error(), ""
			return m, nil
		}
		m.trouble, m.said = "", msg.what+" landed in "+msg.into
		return m, nil

	case removed:
		if msg.err != nil {
			m.trouble, m.said = msg.err.Error(), ""
			return m, nil
		}
		m.trouble, m.said, m.loading = "", "removed "+msg.what, true
		return m, m.listing()

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

	case talking:
		m.typingAt = msg.talk
		return m, nil

	case framePainted:
		if m.screen == nil {
			return m, nil
		}
		return m, waitForFrame(m.screen)

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
	// A terminal with focus takes every key there is, including esc and q — it is a terminal, and
	// half a keyboard is not one. Ctrl+] gives it back, which is what telnet chose for the same
	// reason and what fingers already know.
	if m.atKeyboard {
		if msg.Type == tea.KeyCtrlCloseBracket {
			m.atKeyboard = false
			return m, nil
		}
		if m.typingAt != nil {
			_ = m.typingAt.Type(keyBytes(msg))
		}
		return m, nil
	}

	// A removal on another machine waits for a yes. Every other key here acts on one keystroke,
	// which is no way to take somebody's file off their disk.
	if m.removing != "" {
		return m.answering(msg.String())
	}

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
		if m.at == levelUsers {
			m.stop()
			return m, tea.Quit
		}
		return m.back_()

	case "esc", "left", "h":
		return m.back_()

	case "enter", "right", "l":
		return m.enter()

	case "i":
		at, ok := m.path()
		if !ok || m.at != levelOpen {
			return m, nil
		}

		switch m.showing(at) {
		case showsTalk:
			m.writing = true

		// A live path that takes input is typed into the same way, by saying so first. Everything
		// after goes to it rather than to this list.
		case showsLive:
			if at.Writable && m.typingAt != nil {
				m.atKeyboard, m.trouble = true, ""
			}
		}
		return m, nil

	case "s":
		// One key wherever something is sent, because it is the same act: name a thing and send it.
		if m.at == levelBrowse {
			return m.browsing("s")
		}
		if at, ok := m.path(); ok && m.at == levelOpen && m.showing(at) == showsPut {
			m.putting, m.typing, m.options, m.said, m.trouble = true, "", nil, "", ""
		}
		return m, nil

	case "g":
		if m.at != levelBrowse {
			return m, nil
		}
		return m.browsing("g")

	case "p":
		if m.at == levelUsers && m.linking == nil {
			return m, offer(m.back)
		}
		return m, nil

	case "t":
		// Take a code on the device list; on the management screen it is what changes trust.
		if m.at == levelManage {
			return m, trusting(m.back, m.managed.Name, !m.managed.Trusted)
		}
		if m.at == levelUsers && m.linking == nil {
			m.joining, m.typing, m.trouble = true, "", ""
		}
		return m, nil

	case "r":
		m.loading = true
		if m.at == levelBrowse {
			return m, m.listing()
		}
		if m.at == levelPaths {
			if with, ok := m.peer(); ok {
				return m, loadPaths(m.back, with)
			}
		}
		return m, loadPeers(m.back)

	case "m":
		// Managing somebody, as against reaching them. Trust and grants belong to a user, so this
		// is a user's screen — except under anon, where there is no person and the machine is the
		// only thing there is to manage.
		var who string
		switch {
		case m.at == levelUsers:
			if it, ok := m.list.SelectedItem().(userItem); ok && !it.anon {
				who = it.name
			}
		case m.at == levelMachines && m.atUser == Anon:
			if it, ok := m.list.SelectedItem().(deviceItem); ok && !it.self {
				who = it.entry.Name
			}
		case m.at == levelMachines:
			who = m.atUser
		}
		if who == "" || who == Me {
			return m, nil
		}

		m.at, m.loading, m.trouble = levelManage, true, ""
		m.managed = Managed{Name: who}
		m.showManage()
		return m, loadManaged(m.back, who)

	case "f":
		if m.at != levelManage {
			return m, nil
		}
		return m, forgetting(m.back, m.managed.Name)

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
		// In a directory, x is what takes something off the far machine. On somebody else's locked
		// path, a is how you ask for it. Everywhere else the three keys belong to the access list.
		if m.at == levelBrowse {
			if msg.String() != "x" {
				return m, nil
			}
			return m.browsing("x")
		}

		if m.at == levelPaths && !m.onSelf && msg.String() == "a" {
			// Whatever the cursor is on, not whatever was last entered.
			row, okPath := m.list.SelectedItem().(pathItem)
			with, okPeer := m.peer()
			if okPath && okPeer && row.step.served.Locked {
				at := row.step.served.Path
				m.trouble, m.said = "", "asking for "+at+"…"
				return m, ringFor(m.back, with, at)
			}
			return m, nil
		}

		if m.at == levelManage {
			row, ok := m.onManaged()
			if !ok || row.path == "" {
				return m, nil
			}

			to := Allowed
			switch msg.String() {
			case "x":
				to = Refused
			case "d":
				to = NotNamed
			}
			return m, changeThen(m.back, row.path, m.managed.Name, to, m.managed.Name)
		}

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

// enter goes one level deeper: a user, then one of their machines, then a path, then the thing.
func (m Model) enter() (tea.Model, tea.Cmd) {
	switch m.at {
	case levelUsers:
		it, ok := m.list.SelectedItem().(userItem)
		if !ok {
			// A divider or a device that merely knocked. Neither is a thing to enter.
			return m, nil
		}

		m.at, m.atUser, m.trouble = levelMachines, it.name, ""
		m.showMachines()
		return m, nil

	case levelMachines:
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

		// A namespace that is a directory is walked at its own level, where the list carries the
		// arrows and the filtering.
		if here, _ := m.path(); m.showing(here) == showsWalk {
			m.at, m.dir, m.held = levelBrowse, "", nil
			m.loading, m.trouble, m.said = true, "", ""
			m.showBrowse()
			return m, m.listing()
		}

		m.at = levelOpen
		return m, m.openPath()

	case levelBrowse:
		return m.walkInto()
	}
	return m, nil
}

// back_ comes out one level, stopping whatever the level was doing.
func (m Model) back_() (tea.Model, tea.Cmd) {
	switch m.at {
	case levelManage:
		// Back to wherever it was opened from, which is the users screen unless it was reached
		// from inside somebody's machines.
		if m.atUser != "" {
			m.at, m.trouble = levelMachines, ""
			m.showMachines()
			return m, nil
		}
		m.at, m.trouble = levelUsers, ""
		m.showUsers()
		return m, nil

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

	case levelBrowse:
		return m.walkOut()

	case levelPaths:
		// Out of a folder before out of the device: walking in three levels and being thrown all
		// the way out is not what going back means anywhere else.
		if m.under != "/" && m.under != "" {
			m.under, m.atPath = up(m.under), 0
			m.showPaths()
			return m, nil
		}

		m.at, m.onSelf = levelMachines, false
		m.paths, m.trouble = nil, ""
		m.showMachines()
		return m, nil

	case levelMachines:
		m.at, m.atUser, m.trouble = levelUsers, "", ""
		m.showUsers()
		return m, nil
	}
	return m, nil
}

// showDevices puts the address book in the list, grouped.
// showMachines puts one user's machines in the list.
func (m *Model) showMachines() {
	items, from := machinesOf(m.me, m.peers, m.rows.under[m.atUser], m.atUser, m.reaching)

	m.ofUser = from
	m.list.SetItems(items)
	m.list.Select(m.rowFor(m.atPeer))
	m.list.SetSize(m.listWidth(), m.listHeight())
}

func (m *Model) showUsers() {
	m.rows = group(m.me, m.peers, m.reaching, m.knocked)
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
	if !okPath {
		return nil
	}
	with, okPeer := m.peer()
	if !okPeer {
		return nil
	}

	switch m.showing(at) {
	case showsTalk, showsPut:
		// One conversation behind both: what was said, what changed hands and what was opened are
		// one record, and a namespace something is sent to is a view onto part of it.
		return loadHistory(m.back, with)

	case showsLive:
		m.screen = newScreen(m.viewWidth(), m.viewHeight())
		m.live, m.typingAt = true, nil

		ctx, stop := context.WithCancel(context.Background())
		m.stopped = stop

		// The far end hands back a way to speak to it once there is one. Kept where the model can
		// see it, so a window that changes shape says so and a key can be sent.
		said := make(chan Talk, 1)

		return tea.Batch(
			watch(m.back, with, at, m.screen, ctx, func(t Talk) {
				select {
				case said <- t:
				default:
				}
			}),
			waitForFrame(m.screen),
			waitForTalk(said, m.viewWidth(), m.viewHeight()),
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
		if !ok || !viewOf(at.Archetype).onDisk {
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

		of := viewOf(at.Archetype)
		if !of.onDisk {
			return m, putLink(m.back, with, at.Path, at.Archetype, of.kind, body)
		}

		file := expand(body)
		if _, err := os.Stat(file); err != nil {
			m.trouble = "no such file: " + file
			return m, nil
		}
		m.offering = &moving{}

		// In a directory it goes to wherever the browse level is standing; anywhere else the
		// namespace itself is the destination.
		if m.at == levelBrowse {
			return m, tea.Batch(upload(m.back, with, at.Path, m.dir, file, m.offering), ticking())
		}
		return m, tea.Batch(putFile(m.back, with, at.Path, file, m.offering), ticking())

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

// reading reports whether a conversation is open and being read rather than written.
func (m Model) reading() bool {
	if m.at != levelOpen || m.writing || m.putting {
		return false
	}

	at, ok := m.path()
	return ok && m.showing(at) == showsTalk
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

// onSelfRow reports whether the cursor is on this machine, which only exists inside your own user.
func (m Model) onSelfRow() bool {
	it, ok := m.list.SelectedItem().(deviceItem)
	return ok && it.self
}

// rowFor is where a peer sits on the machines screen, and the top when it is not on it.
func (m Model) rowFor(peer int) int {
	for at, from := range m.ofUser {
		if from == peer {
			return at
		}
	}
	return 0
}

// peerFor is which peer a row on the machines screen is, and the one already open otherwise.
func (m Model) peerFor(row int) int {
	if row < 0 || row >= len(m.ofUser) || m.ofUser[row] < 0 {
		return m.atPeer
	}
	return m.ofUser[row]
}
