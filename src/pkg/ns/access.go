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
	// AnyPaired admits any device in the address book.
	AnyPaired bool
	// Named admits those devices, by the name they are filed under.
	Named []string
	// Keys admits bare endpoint ids that never paired.
	Keys []string
	// Password is an argon2id hash. Whoever presents the secret is admitted, whoever they are.
	Password string
	// All requires every rule declared here, rather than any one of them.
	All bool
}

// Declared reports whether this says anything at all. One that says nothing admits nobody.
func (a Access) Declared() bool {
	return a.AnyPaired || len(a.Named) > 0 || len(a.Keys) > 0 || a.Password != ""
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
	// Name is what this device is filed under, empty if it is not in the book.
	Name string
	// Paired says a shared secret exists with it.
	Paired bool
	// Password is what was offered, empty if nothing was.
	Password string
}

// Admits decides whether a caller may reach a path, and says why not when it may not.
//
// Deny is the default at every turn: an access that declares nothing, a rule that cannot be read, a
// caller with no id. Forgetting to write a rule has to close a path rather than open one.
func (a Access) Admits(c Caller) (bool, string) {
	if c.ID == "" {
		return false, "the connection proved no identity"
	}
	if !a.Declared() {
		return false, "nothing here is shared with anyone"
	}

	var rules []rule

	if a.AnyPaired {
		rules = append(rules, rule{"pairing", c.Paired})
	}
	if len(a.Named) > 0 {
		rules = append(rules, rule{"pairing", c.Paired && c.Name != "" && has(a.Named, c.Name)})
	}
	if len(a.Keys) > 0 {
		rules = append(rules, rule{"key", hasFold(a.Keys, c.ID)})
	}
	if a.Password != "" {
		rules = append(rules, rule{"password", c.Password != "" && passwd.Verify(a.Password, c.Password)})
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
		if !m.Access.Declared() || !covers(at, path) {
			continue
		}
		if len(at) > bestLen {
			best, bestLen, found = m.Access, len(at), true
		}
	}
	return best, found
}

// Admits is the whole question, asked of a path.
func (t *Table) Admits(path string, c Caller) (bool, string) {
	rule, found := t.AccessFor(path)
	if !found {
		return false, "nothing here is shared with anyone"
	}
	return rule.Admits(c)
}
