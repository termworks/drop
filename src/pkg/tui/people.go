package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"

	"github.com/bresilla/drop/src/pkg/book"
)

// The device list is grouped, because it holds three different kinds of thing.
//
// Your own machines are not peers in any useful sense -- reaching one is reaching your own disk
// from another chair. Somebody else's machines belong under that person, because that is the unit
// access is granted to. And a machine with no person behind it is neither: a build server, or
// anything else paired with --machine.
const (
	groupMe       = "me"
	groupPeople   = "people"
	groupMachines = "machines"
	// groupSeen is devices that dialled and were refused. Not a rung of anything: a record of an
	// attempt, kept so that letting a bare id in does not mean copying hex out of a log.
	groupSeen = "seen"
)

// grouped arranges the address book for the list: which rows are dividers, which are devices, and
// where each device from the book ended up.
//
// The mapping back is kept rather than computed, because a grouped list has no arithmetic that
// turns a row into a device -- how many labels are above a row depends on who is in the book.
type grouped struct {
	items []list.Item
	// me is the row for this machine.
	me int
	// row is where each peer sits, by its index in the address book.
	row []int
	// peer is which peer a row is, for the rows that are one.
	peer map[int]int
	// reaching is which devices a connection is being held to.
	reaching map[string]bool
}

// group sorts the address book into what the list shows.
func group(self Identity, peers []book.Entry, reaching map[string]bool, knocked []Knock) grouped {
	out := grouped{peer: map[int]int{}, row: make([]int, len(peers)), reaching: reaching}
	for i := range out.row {
		out.row[i] = -1
	}

	// Your own machines first. What this one shares is the thing most often worth checking and the
	// only thing that cannot be seen from anywhere else.
	out.items = append(out.items, dividerItem{label: groupMe})
	out.me = len(out.items)
	out.items = append(out.items, deviceItem{
		entry: book.Entry{Name: self.Name, ID: idOf(self.ID)},
		addr:  "this device",
		self:  true,
	})
	out.take(mine(self, peers), peers, false)

	// Then everybody else, their machines under them, people in name order.
	people, names := byPerson(self, peers)
	if len(names) > 0 {
		out.items = append(out.items, dividerItem{label: groupPeople})
	}
	for _, who := range names {
		out.items = append(out.items, personItem{name: who, of: len(people[who])})
		out.take(people[who], peers, true)
	}

	// The machines that are nobody's.
	if loose := machines(peers); len(loose) > 0 {
		out.items = append(out.items, dividerItem{label: groupMachines})
		out.take(loose, peers, false)
	}

	// And last what has dialled and been turned away.
	if len(knocked) > 0 {
		out.items = append(out.items, dividerItem{label: groupSeen})
		for _, at := range knocked {
			out.items = append(out.items, knockItem{knock: at})
		}
	}
	return out
}

// take adds a run of devices to the list, remembering where each of them landed.
func (g *grouped) take(which []int, peers []book.Entry, under bool) {
	for _, at := range which {
		g.row[at] = len(g.items)
		g.peer[len(g.items)] = at
		g.items = append(g.items, deviceItem{
			entry:    peers[at],
			addr:     addrsOf(peers[at]),
			under:    under,
			reaching: g.reaching[peers[at].Name],
		})
	}
}

// mine is the machines signed by this machine's own user key.
func mine(self Identity, peers []book.Entry) []int {
	if self.User == "" {
		return nil
	}

	var out []int
	for i, p := range peers {
		if p.User == self.User {
			out = append(out, i)
		}
	}
	return out
}

// byPerson groups everybody else's machines under the name their owner is filed as.
func byPerson(self Identity, peers []book.Entry) (map[string][]int, []string) {
	out := map[string][]int{}
	for i, p := range peers {
		if !p.Owned() || p.User == self.User {
			continue
		}
		out[p.Person] = append(out[p.Person], i)
	}

	names := make([]string, 0, len(out))
	for who := range out {
		names = append(names, who)
	}
	sort.Strings(names)
	return out, names
}

// machines is the devices with no person behind them.
func machines(peers []book.Entry) []int {
	var out []int
	for i, p := range peers {
		if !p.Owned() {
			out = append(out, i)
		}
	}
	return out
}

// addrsOf is where a device was last known to be, as one line.
func addrsOf(entry book.Entry) string { return strings.Join(entry.Addrs, "  ") }
