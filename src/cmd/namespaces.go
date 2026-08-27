package cmd

import (
	"fmt"
	"strings"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/ns"
)

// showOwnTable prints what this node serves.
//
// Unfiltered, deliberately: you are not a guest on your own machine, and this is where you check
// that a rule says what you meant it to.
func showOwnTable(known *arch.Registry) error {
	cfg, err := conf.Load(known)
	if err != nil {
		return err
	}
	if _, err := cfg.Grants(); err != nil {
		return err
	}

	created, err := made.Load()
	if err != nil {
		return err
	}
	skipped, err := cfg.Created(created)
	if err != nil {
		return err
	}

	if cfg.Path == "" {
		fmt.Printf("no config file; serving the defaults\n\n")
	} else {
		fmt.Printf("%s\n\n", cfg.Path)
	}

	mounts := cfg.Mounts.All()
	kind := widest(6, kinds(mounts))
	for _, m := range mounts {
		fmt.Printf("  %-24s %-*s %-8s %-22s %s%s%s\n",
			m.Path, kind, kindOf(m.Archetype), m.Source, shared(cfg.Mounts, m), detail(known, m), unresolved(known, m), sharedAs(m))
	}
	shadowed(skipped)
	if said := membership(mounts); said != "" {
		fmt.Printf("\n%s", said)
	}
	return nil
}

// unresolved is what a namespace says about a change it could not merge, for the row it is on.
//
// Asked of the archetype and not worked out here: what counts as unsettled is the archetype's own
// business, and a listing that knew would be a listing with a case per archetype in it.
func unresolved(known *arch.Registry, m ns.Mount) string {
	said := arch.Trouble(known, m.Archetype, m.Version, m.Config)
	if said == "" {
		return ""
	}
	return "  " + said
}

// membership says what the rule column means for a namespace several machines hold.
//
// Said once, under the table, because it is the one thing about holding a thing together that the
// table cannot show: there is no list of who holds it, so the rule is the list. Somebody holding it
// whom the rule does not name is somebody whose changes are passed over, and everything made after
// one of theirs with them, with nothing on the row to say so.
func membership(mounts []ns.Mount) string {
	for _, m := range mounts {
		if m.Shared.Declared() {
			return "  who a shared namespace is open to is who holds it: a change signed by anybody\n" +
				"  else is refused here, and so is everything made after it.\n"
		}
	}
	return ""
}

// sharedAs says a namespace is one several machines hold, and what they all call it.
//
// The name is worth showing because it is what its history is filed under, and because two machines
// that disagree about it are holding two things.
func sharedAs(m ns.Mount) string {
	if !m.Shared.Declared() {
		return ""
	}
	return fmt.Sprintf("  · shared %s", m.Shared.ID()[:12])
}

// shadowed says what was written down and is not being served, one line each. A path that is in the
// file and absent from the table is otherwise a silence somebody has to go and investigate.
func shadowed(skipped []made.Skipped) {
	if len(skipped) == 0 {
		return
	}

	fmt.Println()
	for _, one := range skipped {
		fmt.Printf("  %s\n", one)
	}
}

// widest is how much room a column of these needs, never under a floor, so one long entry moves the
// column rather than pushing the rest of its row out of line.
func widest(floor int, of []string) int {
	width := floor
	for _, text := range of {
		if len(text) > width {
			width = len(text)
		}
	}
	return width
}

// kinds is the archetype column of a table of mounts.
func kinds(mounts []ns.Mount) []string {
	out := make([]string, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, kindOf(m.Archetype))
	}
	return out
}

// shared says who a path is open to, so a config can be read back rather than reasoned about.
func shared(table *ns.Table, m ns.Mount) string {
	// Asked of the table rather than read off the mount, so that what is printed is what a caller
	// is actually judged against -- a rule somebody wrote plus whatever has been granted since.
	rule, found := table.AccessFor(m.Path)
	if !found {
		return "nobody"
	}
	if !m.Access.Declared() {
		// Nothing here, so say what it resolves to rather than "inherited" — a path with nothing
		// above it inherits nothing, and reads as reachable when it is not.
		return "↑ " + describeRule(rule)
	}
	return describeRule(rule)
}

// describeRule says who a rule admits, so a config can be read back rather than reasoned about.
func describeRule(a ns.Access) string {

	var parts []string
	if a.Anyone {
		parts = append(parts, "anyone at all")
	}
	if a.AnyPaired {
		parts = append(parts, "anyone paired")
	}
	if a.AnyTrusted {
		parts = append(parts, "anyone trusted")
	}
	if len(a.Named) > 0 {
		parts = append(parts, strings.Join(a.Named, ", "))
	}
	if len(a.Keys) > 0 {
		parts = append(parts, fmt.Sprintf("%d key(s)", len(a.Keys)))
	}
	if a.Password != "" {
		parts = append(parts, "a password")
	}

	joined := strings.Join(parts, " + ")
	if a.All && len(parts) > 1 {
		joined = "all of: " + joined
	}
	if len(a.Refused) > 0 {
		joined += ", not " + strings.Join(a.Refused, ", ")
	}
	return joined
}
