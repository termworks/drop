package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/bresilla/drop/src/pkg/proto"
)

// Managing people, machines and who may open what, a key at a time.
//
// Every key here is also in the list along the bottom and in the menu space opens, so none of this
// has to be known to be found.

// adminKey is what a key does on the screens that manage rather than reach, and whether it did
// anything: a key it leaves alone goes on to do whatever it does everywhere else.
func (m Model) adminKey(key string) (tea.Model, tea.Cmd, bool) {
	switch m.at {
	case levelUsers:
		return m.usersKey(key)
	case levelMachines:
		return m.machinesKey(key)
	case levelPaths:
		if key == "w" && m.mineOpen() {
			return m.openAccess()
		}
	case levelAccess:
		return m.accessKey(key)
	case levelManage:
		return m.manageKey(key)
	}
	return m, nil, false
}

func (m Model) usersKey(key string) (tea.Model, tea.Cmd, bool) {
	it, onUser := m.list.SelectedItem().(userItem)
	person := onUser && !it.mine && !it.anon

	// A device on this network: added with enter or a, made one of yours with o, or this one made one
	// of its with i. Its person says yes on its screen.
	if near, ok := m.list.SelectedItem().(manageItem); ok && near.act == actNear {
		kind := ""
		switch key {
		case "enter", "a":
			kind = askPair
		case "o":
			kind = askMine
		case "i":
			kind = askJoin
		}
		if kind != "" {
			m.trouble, m.said, m.loading = "", "asking "+near.label+" — say yes on it", true
			return m, inviteTo(m.back, near.who, kind), true
		}
	}

	switch key {
	case "a":
		if m.linking == nil {
			return m, offer(m.back), true
		}
	case "m":
		if onUser && it.mine {
			m.at, m.trouble, m.said = levelManage, "", ""
			m.managed = Managed{Name: Me}
			m.showManage()
			return m, nil, true
		}
		if onUser {
			return m.manageOf(it.name)
		}
	case "n":
		if person {
			return m.renamePrompt(it.name), nil, true
		}
	case "x":
		if person {
			m.confirm = &confirming{
				ask: "remove " + it.name + "? every machine of theirs is forgotten here",
				yes: removingIt(m.back, it.name, "removed "+it.name),
			}
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m Model) machinesKey(key string) (tea.Model, tea.Cmd, bool) {
	it, onMachine := m.list.SelectedItem().(deviceItem)
	other := onMachine && !it.self

	switch key {
	case "a":
		if m.linking == nil {
			return m, offer(m.back), true
		}
	case "o", "i":
		// What a device you added is to you: one of your machines, or this one one of theirs.
		if !other || m.atUser == Me {
			return m, nil, true
		}
		kind, says := askMine, "asking "+it.entry.Name+" to become one of your machines — say yes on it"
		if key == "i" {
			kind, says = askJoin, "asking "+it.entry.Name+" to take this machine into theirs — say yes on it"
		}
		m.trouble, m.said, m.loading = "", says, true
		return m, inviteTo(m.back, it.entry.ID.String(), kind), true
	case "n":
		if other {
			return m.renamePrompt(it.entry.Name), nil, true
		}
	case "x":
		if !other {
			return m, nil, true
		}
		ask, said := "forget "+it.entry.Name+"? it arrives as a stranger from then on", "forgot "+it.entry.Name
		if m.atUser == Me {
			ask = "take " + it.entry.Name + " out of your machines? every one of them turns it away from then on"
			said = it.entry.Name + " is no longer one of your machines"
		}
		m.confirm = &confirming{ask: ask, yes: removingIt(m.back, it.entry.Name, said)}
		return m, nil, true
	case "m":
		switch {
		case m.atUser == Me:
			return m, nil, true
		case m.atUser == Anon && other:
			return m.manageOf(it.entry.Name)
		case m.atUser != Anon:
			return m.manageOf(m.atUser)
		}
	case "t":
		who := m.atUser
		if m.atUser == Anon {
			if !other {
				return m, nil, true
			}
			who = it.entry.Name
		}
		if who == Me {
			return m, nil, true
		}
		return m, trusting(m.back, who, !m.trustedHere(who)), true
	}
	return m, nil, false
}

// trustedHere says whether somebody is trusted, by the address book as last read.
func (m Model) trustedHere(who string) bool {
	for _, p := range m.peers {
		if (p.Person == who || p.Name == who) && p.Trusted {
			return true
		}
	}
	return false
}

// manageOf opens the screen for one person, or one machine that belongs to nobody.
func (m Model) manageOf(who string) (tea.Model, tea.Cmd, bool) {
	m.at, m.loading, m.trouble, m.said = levelManage, true, "", ""
	m.managed, m.opens, m.askingReach = Managed{Name: who}, nil, true
	m.showManage()
	return m, tea.Batch(loadManaged(m.back, who), loadReach(m.back, who)), true
}

// renamePrompt asks for somebody's or something's new name.
func (m Model) renamePrompt(old string) Model {
	m.prompt = &prompting{
		title: "rename " + old,
		says:  "What to call " + old + " here. Nobody else is told; it is your own word for them.",
		done:  func(name string) tea.Cmd { return renaming(m.back, old, name) },
	}
	return m
}

// mineOpen says the paths on screen are on a machine of yours, which is where who may open them
// can be seen and changed.
func (m Model) mineOpen() bool {
	if m.onSelf {
		return true
	}
	with, ok := m.peer()
	return ok && m.me.User != "" && with.User == m.me.User
}

// openAccess opens who may open the path under the cursor, on whichever machine of yours it is.
func (m Model) openAccess() (tea.Model, tea.Cmd, bool) {
	row, ok := m.list.SelectedItem().(pathItem)
	if !ok || !row.step.is {
		return m, nil, true
	}
	machine := ""
	if !m.onSelf {
		with, _ := m.peer()
		machine = with.Name
	}
	m.at, m.loading, m.trouble, m.said = levelAccess, true, "", ""
	m.onMachine, m.detail = machine, PathDetail{PathState: PathState{Path: row.step.served.Path}}
	m.showAccess()
	return m, askPath(m.back, machine, proto.Manage{Op: proto.ManageRead, Path: row.step.served.Path}), true
}

func (m Model) accessKey(key string) (tea.Model, tea.Cmd, bool) {
	row, onRow := m.onManaged()
	ask := func(op string, set func(*proto.Manage)) (tea.Model, tea.Cmd, bool) {
		asked := proto.Manage{Op: op, Path: m.detail.Path}
		if set != nil {
			set(&asked)
		}
		m.loading = true
		return m, askPath(m.back, m.onMachine, asked), true
	}

	switch key {
	case "r":
		return ask(proto.ManageRead, nil)
	case "enter":
		if !onRow {
			return m, nil, true
		}
		switch row.act {
		case actStep:
			return ask(proto.ManageLevel, func(a *proto.Manage) { a.Level = row.level })
		case actShown:
			return ask(proto.ManageShown, func(a *proto.Manage) { a.Shown = !m.detail.Shown })
		case actAsking:
			return ask(proto.ManageAllow, func(a *proto.Manage) { a.Who = row.who })
		}
		return m, nil, true
	case "a", "x", "d":
		if !onRow || (row.act != actWho && row.act != actAsking) || (key == "d" && row.act != actWho) {
			return m, nil, true
		}
		op := map[string]string{"a": proto.ManageAllow, "x": proto.ManageDeny, "d": proto.ManageUnset}[key]
		return ask(op, func(a *proto.Manage) { a.Who = row.who })
	}
	return m, nil, false
}

func (m Model) manageKey(key string) (tea.Model, tea.Cmd, bool) {
	row, onRow := m.onManaged()
	who := m.managed.Name

	// Your own screen does two things, each asked about first.
	if who == Me {
		if key != "enter" || !onRow {
			return m, nil, key != "esc" && key != "q" && key != "?" && key != " " && key != "up" && key != "down" && key != "k" && key != "j"
		}
		switch row.act {
		case actRenew:
			m.loading, m.trouble, m.said = true, "", "touch your key when it blinks, once for each machine"
			return m, renew(m.back), true
		case actLeave:
			m.confirm = &confirming{ask: "leave your machines? this machine goes back to its own", yes: leaving(m.back)}
		case actStartOver:
			m.confirm = &confirming{ask: "delete everything drop knows on this machine? it cannot be brought back", yes: startingOver(m.back)}
		}
		return m, nil, true
	}

	switch key {
	case "r":
		m.askingReach = true
		return m, tea.Batch(loadManaged(m.back, who), loadReach(m.back, who)), true
	case "t":
		return m, trusting(m.back, who, !m.managed.Trusted), true
	case "n":
		return m.renamePrompt(who), nil, true
	case "f":
		m.confirm = &confirming{ask: "remove " + who + "? every machine of theirs is forgotten here", yes: removingIt(m.back, who, "removed "+who)}
		return m, nil, true
	case "enter":
		if !onRow {
			return m, nil, true
		}
		switch row.act {
		case actTrust:
			return m, trusting(m.back, who, !m.managed.Trusted), true
		case actRename:
			return m.renamePrompt(who), nil, true
		case actForget:
			return m.manageKey("f")
		}
		return m, nil, true
	case "a", "x", "d":
		if !onRow || row.act != actOpen || row.called == "" {
			if onRow && row.act == actOpen {
				m.trouble = machineOr(row.machine) + " has never met " + who + ", so nobody there can be let in by name"
			}
			return m, nil, true
		}
		to := map[string]Standing{"a": Allowed, "x": Refused, "d": NotNamed}[key]
		m.askingReach = true
		return m, decidedFor(m.back, row.machine, row.path, row.called, who, to), true
	}
	return m, nil, false
}
