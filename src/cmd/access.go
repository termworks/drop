package cmd

import (
	"context"

	"github.com/bresilla/drop/src/pkg/asked"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/grant"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/tui"
)

// Access is who may reach one of this machine's own paths.
//
// Everybody in the address book is listed, not only those already named: this is where somebody is
// let in, and a list holding only the people already let in would have nowhere to start. What the
// config says and what has been granted here are shown together, because that is how a caller is
// judged -- but the two are read separately, so the interface can say which is which and refuse to
// pretend it can un-write a line somebody typed into their config.
func (l *live) Access(path string) (tui.Rule, error) {
	cfg, err := conf.Load()
	if err != nil {
		return tui.Rule{}, err
	}
	defer cfg.Close()

	// Read before the grants are attached, and again after.
	written, _ := cfg.Mounts.AccessFor(path)
	if _, err := cfg.Grants(); err != nil {
		return tui.Rule{}, err
	}
	rule, _ := cfg.Mounts.AccessFor(path)

	pinned, err := book.Load()
	if err != nil {
		return tui.Rule{}, err
	}

	out := tui.Rule{
		Path:     path,
		Anyone:   rule.Anyone,
		Paired:   rule.AnyPaired,
		Password: rule.Password != "",
	}
	for _, who := range everybody(pinned) {
		who.At = standingIn(rule, who.Name)
		who.InConfig = named(written, who.Name)
		out.Who = append(out.Who, who)
	}

	// Somebody named on this path who is in no address book: a bare id let in once, or a name
	// written by hand for a machine that has not paired yet.
	out.Who = append(out.Who, strangers(rule, written, out.Who)...)

	out.Seen = rule.Shows()
	out.Asked, err = waiting(path)
	if err != nil {
		return tui.Rule{}, err
	}
	return out, nil
}

// waiting is who has rung the bell on a path and not been answered.
func waiting(path string) ([]tui.Wanting, error) {
	all, err := asked.All()
	if err != nil {
		return nil, err
	}

	var out []tui.Wanting
	for _, one := range all {
		if one.Path != path {
			continue
		}
		out = append(out, tui.Wanting{
			Who:  one.Who(),
			Why:  one.Why,
			When: one.At.Local().Format("2 Jan 15:04"),
		})
	}
	return out, nil
}

func (l *live) Grant(path, who string) error  { return editGrant(path, who, grantAllow) }
func (l *live) Refuse(path, who string) error { return editGrant(path, who, grantDeny) }
func (l *live) Unset(path, who string) error  { return editGrant(path, who, grantForget) }

// What editGrant is being asked to do.
const (
	grantAllow = iota
	grantDeny
	grantForget
)

func editGrant(path, who string, how int) error {
	// Allowing and refusing both answer a request, so both take it off the list -- the same call
	// the command line makes, so the interface and the command cannot disagree about what an
	// answer means.
	switch how {
	case grantAllow:
		return answering(path, who, true)
	case grantDeny:
		return answering(path, who, false)
	}

	store, err := grant.Load()
	if err != nil {
		return err
	}
	return store.Forget(path, who)
}

// everybody is the address book as the access list shows it: each person once, and each machine
// that belongs to nobody on its own.
func everybody(pinned *book.Book) []tui.Who {
	seen := map[string]int{}

	var out []tui.Who
	for _, entry := range pinned.All() {
		if !entry.Owned() {
			out = append(out, tui.Who{Name: entry.Name})
			continue
		}

		who := entry.Person
		if at, ok := seen[who]; ok {
			out[at].Machines++
			continue
		}
		seen[who] = len(out)
		out = append(out, tui.Who{Name: who, Person: true, Machines: 1})
	}
	return out
}

// standingIn is how a rule stands towards one name.
func standingIn(rule ns.Access, name string) tui.Standing {
	switch {
	case has(rule.Refused, name):
		return tui.Refused
	case has(rule.Named, name):
		return tui.Allowed
	default:
		return tui.NotNamed
	}
}

// named reports whether a rule names somebody outright.
func named(rule ns.Access, name string) bool { return has(rule.Named, name) }

func has(list []string, want string) bool {
	for _, at := range list {
		if at == want {
			return true
		}
	}
	return false
}

// strangers is everybody a rule names who is in no address book.
func strangers(rule, written ns.Access, known []tui.Who) []tui.Who {
	var out []tui.Who
	for _, names := range [][]string{rule.Named, rule.Refused} {
		for _, name := range names {
			if hasWho(known, name) || hasWho(out, name) {
				continue
			}
			out = append(out, tui.Who{
				Name:     name,
				At:       standingIn(rule, name),
				InConfig: named(written, name),
			})
		}
	}
	return out
}

func hasWho(all []tui.Who, name string) bool {
	for _, who := range all {
		if who.Name == name {
			return true
		}
	}
	return false
}

// AskFor rings the bell on a path this machine can see but not open.
func (l *live) AskFor(ctx context.Context, on book.Entry, path, why string) error {
	stream, err := l.held.To(ctx, on, node.ALPNSession)
	if err != nil {
		return err
	}
	defer stream.Close()

	return proto.Ask(ctx, stream, path, why, node.DisplayName())
}
