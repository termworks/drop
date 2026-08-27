// Package among is who else holds a namespace several machines hold.
//
// There is no list of members anywhere, and nothing here keeps one. The access rule already names
// the people a namespace is shared with, and the address book already says which machines are
// theirs, so the rule is the membership: whoever the rule admits holds it, and changing the rule
// changes who does. A second list beside it would be a second place to forget to look, and the two
// would disagree the first time somebody granted access from the interface.
//
// It follows that membership is each machine's own answer rather than one everybody shares. A
// machine takes changes from the people its own rule names, which is the same sentence as "a
// machine answers to the people its own rule names", and it is why removing somebody stops their
// next change rather than unsaying their last one.
package among

import (
	"sort"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/ns"
)

// Holders is every machine in the address book the rule admits, in the order a person reads them.
//
// A namespace shared behind a password has none: nobody is admitted by a rule they have not
// answered, and there is nothing to answer with when nobody is asking.
func Holders(rule ns.Access, b *book.Book) []book.Entry {
	if b == nil {
		return nil
	}

	var out []book.Entry
	for _, entry := range b.All() {
		if ok, _ := rule.Admits(caller(entry)); ok {
			out = append(out, entry)
		}
	}
	return out
}

// Holds reports whether one machine is one of them.
func Holds(rule ns.Access, e book.Entry) bool {
	ok, _ := rule.Admits(caller(e))
	return ok
}

// People is who holds it, by the key they sign with, this machine's own person included.
//
// The people rather than their machines, because this is what travels: a name in an address book
// is one machine's private label for somebody and means nothing on another, and a key is the same
// everywhere. Whoever reads this list looks each key up in their own book and calls the person
// whatever they call them.
func People(rule ns.Access, b *book.Book, mine string) []string {
	seen := map[string]bool{}
	if mine != "" {
		seen[mine] = true
	}
	for _, entry := range Holders(rule, b) {
		if entry.User != "" {
			seen[entry.User] = true
		}
	}

	out := make([]string, 0, len(seen))
	for key := range seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Admits decides whether a change signed by somebody may be taken.
//
// Checking that the signature is really theirs is the history's job and is already done by the time
// this is asked. This is the other half: whether the person it turned out to be was allowed to
// change this thing. Their own key always is — a machine refusing its owner's changes would refuse
// everything it wrote on another machine of theirs.
//
// The book is walked entry by entry, the same way Holders walks it, so the two halves of membership
// give one answer. A change is signed by a person and carries no machine, so a rule that names one
// of somebody's machines is read as naming them: whoever the rule would let in is somebody whose
// changes come in. Folding their machines into one first would judge them by a machine they may not
// have been at, and a namespace shared with one machine of somebody's would take nothing from them
// ever again.
func Admits(rule ns.Access, b *book.Book, mine string) func(author string) bool {
	return func(author string) bool {
		if author == "" {
			return false
		}
		if mine != "" && author == mine {
			return true
		}
		if b == nil {
			return false
		}
		for _, entry := range b.All() {
			if entry.User != author {
				continue
			}
			if ok, _ := rule.Admits(caller(entry)); ok {
				return true
			}
		}
		return false
	}
}

// caller is one address book entry as an access rule judges it.
//
// Nothing is being decided about a live connection here: the question is whether a rule names this
// person, and the answer has to be the same one it would give if their machine dialled.
func caller(e book.Entry) ns.Caller {
	return ns.Caller{
		ID:       e.ID.String(),
		Name:     e.Name,
		Paired:   e.Paired(),
		Trusted:  e.Trusted,
		User:     e.User,
		UserName: e.Person,
	}
}
