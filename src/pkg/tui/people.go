package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"

	"github.com/bresilla/drop/src/pkg/book"
)

// The first screen is users. Machines are inside them.
//
// A user is the unit everything else is written against: access rules name people, trust belongs to
// people, and a machine somebody buys next week is already covered. Putting machines on the first
// screen made it as long as the number of laptops in your life, and put the thing you grant to a
// level below the thing you look at.
//
// Every machine has a user, including the ones that do not: a device paired with --machine belongs
// to nobody, and nobody is a user called anon. That keeps the screen one kind of thing rather than
// three, and it keeps you symmetric with everybody else — me is a user with machines in it, exactly
// as bob is.

// Anon is the user a machine paired on its own belongs to, and Me is what you are called here.
const (
	Anon = "anon"
	Me   = "me"
)

// groupSeen heads the devices that dialled and were refused. Not a user: a record of an attempt,
// kept so that letting a bare id in does not mean copying hex out of a log.
const groupSeen = "seen"

// users arranges the address book into people, and remembers which machines are whose.
type users struct {
	items []list.Item
	// who is the name at each row, for the rows that are a user.
	who map[int]string
	// row is where each user sits, by name.
	row map[string]int
	// under is every machine of a user, as indices into the address book.
	under map[string][]int
	// order is the users in the order they are shown.
	order []string
}

// group sorts the address book into the users screen.
func group(self Identity, peers []book.Entry, reaching map[string]bool, knocked []Knock) users {
	out := users{
		who:   map[int]string{},
		row:   map[string]int{},
		under: map[string][]int{},
	}

	for i, p := range peers {
		who := userOf(self, p)
		out.under[who] = append(out.under[who], i)
	}

	// You first, then everybody else by name, then the machines that belong to nobody. Anon last
	// because it is the odd one: a user that is not a person.
	var people []string
	for who := range out.under {
		if who == Me || who == Anon {
			continue
		}
		people = append(people, who)
	}
	sort.Strings(people)

	out.order = append([]string{Me}, people...)
	if len(out.under[Anon]) > 0 {
		out.order = append(out.order, Anon)
	}

	for _, who := range out.order {
		of := out.under[who]

		out.row[who] = len(out.items)
		out.who[len(out.items)] = who
		out.items = append(out.items, userItem{
			name: who,
			// You always have at least this machine, which is not in the address book.
			of:       len(of) + countSelf(who),
			trusted:  trustedIn(peers, of),
			mine:     who == Me,
			anon:     who == Anon,
			reaching: reachingIn(peers, of, reaching) + countSelf(who),
		})
	}

	// And last what has dialled and been turned away, which is nobody's user.
	if len(knocked) > 0 {
		out.items = append(out.items, dividerItem{label: groupSeen})
		for _, at := range knocked {
			out.items = append(out.items, knockItem{knock: at})
		}
	}
	return out
}

// userOf is the user a machine belongs to.
func userOf(self Identity, p book.Entry) string {
	switch {
	case !p.Owned():
		return Anon
	case self.User != "" && p.User == self.User:
		return Me
	case p.Person != "":
		return p.Person
	default:
		return p.Name
	}
}

// countSelf is the machine this interface is running on, which is yours and is not in the book.
func countSelf(who string) int {
	if who == Me {
		return 1
	}
	return 0
}

func trustedIn(peers []book.Entry, of []int) bool {
	for _, at := range of {
		if peers[at].Trusted {
			return true
		}
	}
	return false
}

func reachingIn(peers []book.Entry, of []int, reaching map[string]bool) int {
	out := 0
	for _, at := range of {
		if reaching[peers[at].Name] {
			out++
		}
	}
	return out
}

// machinesOf is one user's machines, as rows, and which peer each row came from.
//
// Your own screen carries this machine first: it is yours, it is the one thing that cannot be seen
// from anywhere else, and leaving it out is what made your own user the odd one out.
func machinesOf(self Identity, peers []book.Entry, of []int, who string, reaching map[string]bool) ([]list.Item, []int) {
	var items []list.Item
	var from []int

	if who == Me {
		items = append(items, deviceItem{
			entry: book.Entry{Name: self.Name, ID: idOf(self.ID)},
			addr:  "this device",
			self:  true,
		})
		from = append(from, -1)
	}

	for _, at := range of {
		items = append(items, deviceItem{
			entry:    peers[at],
			addr:     addrsOf(peers[at]),
			reaching: reaching[peers[at].Name],
		})
		from = append(from, at)
	}
	return items, from
}

// addrsOf is where a device was last known to be, as one line.
func addrsOf(entry book.Entry) string { return strings.Join(entry.Addrs, "  ") }
