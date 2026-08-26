package ns

import (
	"fmt"
	"strings"

	"github.com/bresilla/drop/src/pkg/passwd"
)

// Access is who may reach a path.
//
// Three ways to satisfy it, and a path may declare any mix. Two of them bind to a key the far end
// proved it holds during the handshake; the third binds only to knowledge, which is why it is the
// one worth thinking twice about.
type Access struct {
	// AnyPaired admits any device in the address book, and AnyTrusted only the ones you decided
	// to trust. Pairing is recognition, so "paired" is a wide rule; "trusted" is the narrow one.
	AnyPaired  bool
	AnyTrusted bool
	// Named admits people and machines, by the name they are filed under.
	//
	// A name on its own means a person: any machine of theirs. A name with a machine after it —
	// "bob@laptop" — means that machine and no other. A device paired with --machine belongs to
	// nobody here and answers to its own name, which is the plain form again.
	Named []string
	// Anyone admits any caller at all, whether or not it has ever paired. A public path: whoever
	// learns this device's id can reach it.
	Anyone bool
	// Keys admits bare endpoint ids that never paired.
	Keys []string
	// Password is an argon2id hash. Whoever presents the secret is admitted, whoever they are.
	Password string
	// All requires every rule declared here, rather than any one of them.
	All bool
	// Visible is who may see this path without being able to open it, and AnyVisible is everybody
	// paired. A path that is visible says it exists and refuses to be opened, so somebody can ask
	// for it by name instead of having to be told it is there.
	//
	// It is the rung between shared and secret. A folder made later shows up for the people it is
	// meant for, and they ask; nobody has to paste a path around, and nobody who was not meant to
	// see it learns that it exists.
	Visible    []string
	AnyVisible bool
	// TrustedVisible shows it to the people you decided to trust, and to nobody else you have
	// merely met. This is the ordinary way to put something up to be asked for.
	TrustedVisible bool
	// Refused is who may not reach this path whatever else says otherwise. It is not written in
	// the config: it is what revoking from the interface leaves behind, and it beats every rule
	// above, which is what makes it take effect on the next connection rather than in ninety days.
	Refused []string
}

// Declared reports whether this says anything at all. One that says nothing admits nobody.
func (a Access) Declared() bool {
	return a.Anyone || a.AnyPaired || a.AnyTrusted ||
		len(a.Named) > 0 || len(a.Keys) > 0 || a.Password != ""
}

// Sees reports whether a caller may know this path exists, without being able to open it.
//
// Anybody admitted can see it, obviously. Beyond that a path may be made visible on its own terms,
// which is what lets somebody ask for something rather than be handed it.
func (a Access) Sees(c Caller) bool {
	if ok, _ := a.Admits(c); ok {
		return true
	}
	if refused(a.Refused, c) {
		return false
	}
	if a.AnyVisible && c.Paired {
		return true
	}
	if a.TrustedVisible && c.Paired && c.Trusted {
		return true
	}
	return named(a.Visible, c)
}

// Shows reports whether this says anything about being seen.
func (a Access) Shows() bool { return a.AnyVisible || a.TrustedVisible || len(a.Visible) > 0 }

// named decides whether a caller is one of the names a rule lists.
//
// A name on its own is a person: "bob" admits any machine bob has signed a badge for. A name with a
// machine after it is that machine: "bob@laptop" admits one of them. A device paired with --machine
// has no person on this side, and is named on its own — the only way to write a rule for a build
// server or anything else that is nobody's personal identity.
func named(names []string, c Caller) bool {
	if !c.Paired {
		return false
	}

	for _, want := range names {
		who, machine, narrowed := strings.Cut(want, "@")

		switch {
		case narrowed:
			// A person and one of their machines. The machine is matched by the name it is filed
			// under, which is what a person reading the rule wrote down.
			if c.UserName != "" && c.UserName == who && c.Name == machine {
				return true
			}

		default:
			// A person, or a machine paired on its own.
			if c.UserName != "" && c.UserName == want {
				return true
			}
			if c.UserName == "" && c.Name != "" && c.Name == want {
				return true
			}
		}
	}
	return false
}

// rule is one declared way in, and whether the caller satisfied it.
type rule struct {
	name string
	ok   bool
}

// Caller is the device asking, and what it presented.
type Caller struct {
	// ID is the endpoint id the handshake proved. Never empty for a real connection.
	ID string
	// Name is what this device is filed under here, empty if it is not in the book. It is the
	// only name anything is decided on: it is what somebody on this machine wrote down.
	Name string
	// Label is what the device calls itself, off its badge. Nobody vouches for it and no rule may
	// be satisfied by it -- it is there so a list can show a machine that has no local name yet.
	Label string
	// Paired says a shared secret exists with it.
	Paired bool
	// Trusted says this is somebody you decided to trust, not merely somebody you have met.
	// Pairing is recognition; trust is the second, deliberate step.
	Trusted bool
	// Password is what was offered, empty if nothing was.
	Password string
	// Tried is where the answer to a guess is remembered for as long as this caller exists.
	//
	// One path asks whether it admits somebody and then, when it does not, whether they may know
	// it is there; both reach the same hash with the same guess, and each costs 64 MiB and three
	// passes. Nil is allowed and merely pays the cost again.
	Tried *passwd.Tried
	// User is who owns this machine, as their user key is written down. Empty when the badge did
	// not check out.
	User string
	// UserName is what that person is filed under, empty if they are not in the book.
	UserName string
}

// Admits decides whether a caller may reach a path, and says why not when it may not.
//
// Deny is the default at every turn: an access that declares nothing, a rule that cannot be read, a
// caller with no id. Forgetting to write a rule has to close a path rather than open one.
func (a Access) Admits(c Caller) (bool, string) {
	if c.ID == "" {
		return false, "the connection proved no identity"
	}

	// Before anything else, and against everything else.
	if refused(a.Refused, c) {
		return false, "this device has been refused here"
	}
	if !a.Declared() {
		return false, "nothing here is shared with anyone"
	}

	var rules []rule

	if a.Anyone {
		rules = append(rules, rule{"being anybody at all", true})
	}
	if a.AnyPaired {
		rules = append(rules, rule{"pairing", c.Paired})
	}
	if a.AnyTrusted {
		rules = append(rules, rule{"being trusted", c.Paired && c.Trusted})
	}
	if len(a.Named) > 0 {
		rules = append(rules, rule{"pairing", named(a.Named, c)})
	}
	if len(a.Keys) > 0 {
		rules = append(rules, rule{"key", hasFold(a.Keys, c.ID)})
	}
	if a.Password != "" {
		rules = append(rules, rule{"password", c.Password != "" && c.Tried.Says(a.Password, c.Password)})
	}

	passed, failed := 0, ""
	for _, r := range rules {
		if r.ok {
			passed++
			continue
		}
		if failed == "" {
			failed = r.name
		}
	}

	if a.All {
		if passed == len(rules) {
			return true, ""
		}
		return false, fmt.Sprintf("this needs %s, and %s did not check out", listOf(rules), failed)
	}

	if passed > 0 {
		return true, ""
	}
	return false, "not shared with you"
}

func listOf(rules []rule) string {
	seen := make([]string, 0, len(rules))
	for _, r := range rules {
		if !has(seen, r.name) {
			seen = append(seen, r.name)
		}
	}
	if len(seen) < 2 {
		return strings.Join(seen, "")
	}
	return strings.Join(seen[:len(seen)-1], ", ") + " and " + seen[len(seen)-1]
}

func has(list []string, want string) bool {
	for _, at := range list {
		if at == want {
			return true
		}
	}
	return false
}

func hasFold(list []string, want string) bool {
	for _, at := range list {
		if strings.EqualFold(at, want) {
			return true
		}
	}
	return false
}

// AccessFor resolves the rule that governs a path.
//
// The nearest declaration wins and replaces what is above it, rather than merging with it: a mount
// says what it means, and a rule that quietly combined with an ancestor's would leave a path more
// open than it reads. Nothing declared anywhere above means nobody gets in.
func (t *Table) AccessFor(path string) (Access, bool) {
	path, err := Clean(path)
	if err != nil {
		return Access{}, false
	}

	best, bestLen, found := Access{}, -1, false
	for at, m := range t.mounts {
		// A rule that only makes a path visible still governs it: it says nobody may open this,
		// and these people may know it is here.
		if !(m.Access.Declared() || m.Access.Shows()) || !covers(at, path) {
			continue
		}
		if len(at) > bestLen {
			best, bestLen, found = m.Access, len(at), true
		}
	}
	return t.merge(path, best, found)
}

// Sees is whether a caller may know a path exists, asked of the table.
func (t *Table) Sees(path string, c Caller) bool {
	rule, found := t.AccessFor(path)
	if !found {
		return false
	}
	return rule.Sees(c)
}

// Admits is the whole question, asked of a path.
func (t *Table) Admits(path string, c Caller) (bool, string) {
	rule, found := t.AccessFor(path)
	if !found {
		return false, "nothing here is shared with anyone"
	}
	return rule.Admits(c)
}
