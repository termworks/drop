package ns

// Granting is a second source of access rules.
//
// A rule in the config is structure, written by hand, and a program that edits a hand-written file
// is a program that mangles it eventually. So what the interface writes lives apart from it, as
// data drop owns, and the two are merged when a caller is judged -- the same split as sshd_config
// and authorized_keys.
type Granting interface {
	// For reports the extra names allowed at a path, and the ones refused there. Both cover
	// everything below the path they are written at, the way a mount does.
	For(path string) (allow, deny []string)
}

// Granted points the table at a source of grants. Nil is no grants at all, which is what a node
// with nothing in its interface has.
func (t *Table) Granted(g Granting) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.granted = g
}

// merge folds the grants for a path into the rule declared for it.
//
// Allowing is additive: a grant widens what the config says rather than replacing it, because the
// config is what somebody meant and the grant is what they clicked. Refusing is not: it is the only
// thing here that takes effect against a rule somebody wrote, which is what makes it revocation.
func (t *Table) merge(path string, rule Access, found bool) (Access, bool) {
	t.mu.RLock()
	granting := t.granted
	t.mu.RUnlock()

	if granting == nil {
		return rule, found
	}

	allow, deny := granting.For(path)
	rule.Refused = deny

	if len(allow) > 0 {
		rule.Named = append(append([]string(nil), rule.Named...), allow...)
		found = true
	}
	return rule, found || len(deny) > 0
}

// refused decides whether a caller is on a refusal list.
//
// The names are spelt the way an access rule spells them, so refusing "bob" refuses every machine
// of his and refusing "bob@phone" refuses one. Unlike admitting, a refusal does not need the caller
// to be paired: somebody being revoked is very often somebody who still holds a pairing secret, and
// the id is refused outright as well.
func refused(names []string, c Caller) bool {
	if len(names) == 0 {
		return false
	}
	if has(names, c.ID) {
		return true
	}
	return named(names, Caller{ID: c.ID, Name: c.Name, UserName: c.UserName, Paired: true})
}
