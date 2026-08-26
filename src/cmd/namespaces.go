package cmd

import (
	"fmt"

	"strings"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/ns"
)

func newNamespacesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ns",
		Aliases: []string{"namespaces"},
		Short:   "Show the namespaces this node serves",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showOwnTable(reading())
		},
	}
}

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

	if cfg.Path == "" {
		fmt.Printf("no config file; serving the defaults\n\n")
	} else {
		fmt.Printf("%s\n\n", cfg.Path)
	}

	mounts := cfg.Mounts.All()
	kind := widest(6, kinds(mounts))
	for _, m := range mounts {
		fmt.Printf("  %-24s %-*s %-22s %s\n",
			m.Path, kind, kindOf(m.Archetype), shared(cfg.Mounts, m), detail(known, m))
	}
	return nil
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
