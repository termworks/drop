package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// Managing somebody, rather than reaching them.
//
// The device list answers "what can I open"; this answers "who is this, and what have I given
// them". They are different questions and putting both on one screen made neither readable: the
// list grew columns nobody could scan, and the things you do rarely -- trust, forget, revoke --
// sat next to the thing you do constantly, which is open a conversation.

// Managed is somebody as the management screen sees them.
type Managed struct {
	// Name is what they are filed under here, and Person is who owns the machine when that is
	// known. For a person's own row the two are the same.
	Name   string
	Person string
	// ID is the device, empty on a row that stands for a person rather than one machine.
	ID string
	// User is their user key, written the way authorized_keys writes one.
	User string
	// Machines is how many machines they have, for a person.
	Machines int
	Paired   bool
	Trusted  bool
	// Reaching says a connection is being held to them right now.
	Reaching bool
	// Allowed is every path they have been granted here, and Refused every path they are shut out
	// of. Both are what this machine decided, not what the far end thinks.
	Allowed []string
	Refused []string
}

// manageItem is one row of the management screen.
type manageItem struct {
	// what is the heading this row falls under, so the list can be rebuilt in place.
	what string
	// label is the left-hand text, and note the line under it.
	label string
	note  string
	// path is set on the rows that stand for a path, and machine is which machine of yours it is
	// on, the empty one being this; called is what that machine calls whoever the row is about.
	path    string
	machine string
	called  string
	// act is what enter and the letters do to the row, level the step a step row puts a path on,
	// and who the name a row lets in or keeps out.
	act   string
	level string
	who   string
	// on marks a row that is in effect: trusted, allowed, held.
	on bool
	// off marks one that is refused, as against merely absent.
	off bool
}

func (m manageItem) FilterValue() string { return m.label }

// Headings on the management screen.
const (
	groupWho   = "who they are"
	groupCan   = "what they may open"
	groupWhat  = "what you can do"
	groupTrust = "trust"
)

// manageRows arranges somebody for the list: who they are, whether you trust them, what they may
// open on every machine of yours, and what else can be done about them.
func manageRows(who Managed, reach []Reachable, asking bool) []list.Item {
	var items []list.Item

	items = append(items, dividerItem{label: groupWho})
	items = append(items, manageItem{what: groupWho, label: who.Name, note: describeWho(who)})

	// Trust, which is the one thing on this screen that changes what the steps do.
	items = append(items, dividerItem{label: groupTrust})
	items = append(items, manageItem{
		what:  groupTrust,
		label: trustSays(who.Trusted),
		note:  "paths on Trusted open for people you trust — enter or t changes it",
		act:   actTrust,
		on:    who.Trusted,
	})

	items = append(items, dividerItem{label: groupCan})
	if asking && len(reach) == 0 {
		items = append(items, manageItem{what: groupCan, label: "asking your machines…", note: "each of them says what they may open there"})
	}
	items = append(items, reachRows(reach)...)

	items = append(items, dividerItem{label: groupWhat})
	items = append(items,
		manageItem{what: groupWhat, label: "rename", note: "file them under another name here — n", act: actRename},
		manageItem{what: groupWhat, label: "remove them", note: "every machine of theirs is forgotten here, and arrives as a stranger — x", act: actForget},
	)
	return items
}

// describeWho is the line under somebody's name.
func describeWho(who Managed) string {
	switch {
	case who.Machines > 1:
		return fmt.Sprintf("a person, with %d machines", who.Machines)
	case who.Person != "" && who.Person != who.Name:
		return "a machine of " + who.Person + "'s"
	case who.User != "":
		return "a person, with one machine"
	default:
		return "a machine that belongs to nobody here"
	}
}

func trustSays(trusted bool) string {
	if trusted {
		return "trusted"
	}
	return "not trusted"
}

// managing carries somebody's details back from the backend.
type managing struct {
	who Managed
	err error
}

// loadManaged asks what is known about somebody.
func loadManaged(back Backend, name string) tea.Cmd {
	return func() tea.Msg {
		who, err := back.Managed(name)
		return managing{who: who, err: err}
	}
}

// showManage puts somebody in the list, keeping the cursor where it was.
func (m *Model) showManage() {
	at := m.list.Index()
	m.fill("manage\x00"+m.managed.Name, manageRows(m.managed, m.opens, m.askingReach))
	m.list.Select(at)
	m.offHeading()
	m.list.SetSize(m.listWidth(), m.listHeight())
}

// onManaged is the row the cursor is on, when it is one.
func (m Model) onManaged() (manageItem, bool) {
	it, ok := m.list.SelectedItem().(manageItem)
	return it, ok
}

// managed says a change was made to somebody, and carries the name to read back.
type managed struct {
	name string
	// gone marks a pairing that was dropped, so there is nobody left to read back.
	gone bool
	err  error
}

// trusting marks somebody trusted or not.
func trusting(back Backend, name string, to bool) tea.Cmd {
	return func() tea.Msg {
		return managed{name: name, err: back.Trust(name, to)}
	}
}
