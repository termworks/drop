package tui

import (
	"sort"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// Rule is who may reach one of this machine's own paths.
//
// Two sources, kept apart the way they are kept apart on disk: what the config says, which is
// structure somebody wrote, and what has been granted here, which is data drop owns. The interface
// shows them together because that is how a caller is judged, and edits only the second.
type Rule struct {
	Path string
	// Anyone admits whoever knows this device's id, and Paired admits the address book. Both come
	// from the config and are shown so that the list is not read as the whole story.
	Anyone bool
	Paired bool
	// Password says a secret guards this path.
	Password bool
	// Who is everybody the list can say something about: named in the config, granted here, or
	// simply in the address book and therefore somebody you might want to let in.
	Who []Who
}

// Standing is how somebody stands with a path.
type Standing int

const (
	// NotNamed is somebody the path says nothing about. Whether they get in is decided by the
	// wider rules, if there are any.
	NotNamed Standing = iota
	// Allowed is somebody named, in the config or by a grant made here.
	Allowed
	// Refused is somebody on the refusal list, which beats everything else.
	Refused
)

// Who is one person or machine, and how they stand with a path.
type Who struct {
	// Name is how a rule spells them: "bob", "bob@laptop", or an endpoint id.
	Name string
	// Person marks somebody rather than a machine.
	Person bool
	// Machines is how many machines they have, for a person.
	Machines int
	At       Standing
	// InConfig marks somebody the config names, whom the interface cannot un-name -- only refuse.
	InConfig bool
}

// accessItem is one row of the access pane.
type accessItem struct {
	who Who
	// group is the heading this row falls under, kept so the list can be rebuilt in place.
	group string
}

func (a accessItem) FilterValue() string { return a.who.Name }

// accessRows arranges a rule for the list: people, then machines, then the ways in that name
// nobody at all.
func accessRows(rule Rule) []list.Item {
	var people, machines []Who
	for _, who := range rule.Who {
		if who.Person {
			people = append(people, who)
			continue
		}
		machines = append(machines, who)
	}

	byName := func(all []Who) {
		sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	}
	byName(people)
	byName(machines)

	var items []list.Item
	if len(people) > 0 {
		items = append(items, dividerItem{label: groupPeople})
		for _, who := range people {
			items = append(items, accessItem{who: who, group: groupPeople})
		}
	}
	if len(machines) > 0 {
		items = append(items, dividerItem{label: groupMachines})
		for _, who := range machines {
			items = append(items, accessItem{who: who, group: groupMachines})
		}
	}

	// The rungs that name nobody. Shown even when off, because a path that is open to anyone who
	// knows its id is the single most important thing this list can tell you.
	items = append(items, dividerItem{label: groupAnyone})
	items = append(items,
		accessItem{who: Who{Name: "anyone with the id", At: onWhen(rule.Anyone)}, group: groupAnyone},
		accessItem{who: Who{Name: "anyone paired", At: onWhen(rule.Paired)}, group: groupAnyone},
		accessItem{who: Who{Name: "whoever has the password", At: onWhen(rule.Password)}, group: groupAnyone},
	)
	return items
}

func onWhen(on bool) Standing {
	if on {
		return Allowed
	}
	return NotNamed
}

// groupAnyone heads the rungs that admit somebody without naming them.
const groupAnyone = "anyone"

// ruleLoaded carries who may reach a path back from the backend.
type ruleLoaded struct {
	rule Rule
	err  error
}

// loadRule asks who may reach one of this machine's own paths.
func loadRule(back Backend, path string) tea.Cmd {
	return func() tea.Msg {
		rule, err := back.Access(path)
		return ruleLoaded{rule: rule, err: err}
	}
}

// changed says a grant was written, and carries the path to re-read.
type changed struct {
	path string
	err  error
}

// change writes one grant and asks for the rule again, so what is on screen is what is on disk
// rather than what the interface believes it just did.
func change(back Backend, path, who string, to Standing) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch to {
		case Allowed:
			err = back.Grant(path, who)
		case Refused:
			err = back.Refuse(path, who)
		default:
			err = back.Unset(path, who)
		}
		return changed{path: path, err: err}
	}
}

// showAccess puts a rule in the list.
func (m *Model) showAccess() {
	m.list.SetItems(accessRows(m.rule))
	m.list.SetSize(m.listWidth(), m.listHeight())
}

// standingOf is the row the cursor is on, and whether it is one that can be changed.
//
// The rules that name nobody cannot: turning a path public is a decision that belongs in the
// config, where it can be read back and reviewed, not behind one keystroke in a list.
func (m Model) standingOf() (accessItem, bool) {
	it, ok := m.list.SelectedItem().(accessItem)
	if !ok || it.group == groupAnyone {
		return accessItem{}, false
	}
	return it, true
}
