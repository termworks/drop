package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/grant"
)

func newGrantCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "grant <path> <who>",
		Short: "Let somebody reach a path",
		Long: "The config says what a path is for; this says who has been let into it since.\n" +
			"They are kept apart so that granting somebody access never rewrites a file you\n" +
			"wrote by hand.\n\n" +
			"Who is spelt the way an access rule spells it: \"bob\" is a person and every\n" +
			"machine of theirs, \"bob@laptop\" is one machine, and an endpoint id is a device\n" +
			"that never paired.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := grant.Load()
			if err != nil {
				return err
			}
			if err := store.Allow(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("%s may reach %s\n", args[1], args[0])
			return nil
		},
	}
}

func newRevokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <path> <who>",
		Short: "Stop somebody reaching a path",
		Long: "A refusal beats every rule, including one written in the config, and takes effect\n" +
			"on the next connection rather than when a badge expires. It is local: this\n" +
			"machine stops trusting them, and nobody else is told.\n\n" +
			"Use --forget to drop them from the list entirely, leaving whatever the config\n" +
			"says about them.",
		Args: cobra.ExactArgs(2),
	}

	forget := cmd.Flags().Bool("forget", false, "drop them from the list rather than refusing them")
	cmd.RunE = func(*cobra.Command, []string) error {
		store, err := grant.Load()
		if err != nil {
			return err
		}

		args := cmd.Flags().Args()
		if *forget {
			if err := store.Forget(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("%s is no longer named on %s\n", args[1], args[0])
			return nil
		}
		if err := store.Deny(args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("%s is refused at %s\n", args[1], args[0])
		return nil
	}
	return cmd
}

func newGrantsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "grants",
		Short: "What has been allowed and refused from the interface",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return showGrants()
		},
	}
}

// showGrants prints the grants beside what the config says, because a grant on its own does not
// tell you who can reach a path -- only what was added to whoever already could.
func showGrants() error {
	store, err := grant.Load()
	if err != nil {
		return err
	}

	rules := store.Paths()
	if len(rules) == 0 {
		fmt.Println("nothing has been granted or refused")
		return nil
	}

	cfg, err := conf.Load(reading())
	if err != nil {
		return err
	}
	if _, err := cfg.Grants(); err != nil {
		return err
	}

	paths := make([]string, 0, len(rules))
	for at := range rules {
		paths = append(paths, at)
	}
	sort.Strings(paths)

	for _, at := range paths {
		fmt.Printf("\n  %s\n", at)
		for _, who := range rules[at].Allow {
			fmt.Printf("    + %s\n", who)
		}
		for _, who := range rules[at].Deny {
			fmt.Printf("    - %s\n", who)
		}
		if rule, found := cfg.Mounts.AccessFor(at); found {
			fmt.Printf("    = %s\n", describeRule(rule))
		}
	}
	fmt.Println()
	return nil
}
