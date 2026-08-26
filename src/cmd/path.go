package cmd

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/ns"
)

// The namespaces themselves: which a machine serves, who may reach them, and putting one up for a
// moment without writing anything down.

func newPathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "path",
		Short: "The namespaces a machine serves, and who may reach them",
		Long: "What a path is for is declared in the config. Who has been let into it since is\n" +
			"kept apart, so granting somebody access never rewrites a file you wrote by hand.\n\n" +
			"  drop path ls              what this machine serves, and to whom\n" +
			"  drop path ls orin         what orin shares with you\n" +
			"  drop path grant /work bob\n",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	cmd.AddCommand(
		newPathListCmd(),
		newGrantCmd(),
		newRevokeCmd(),
		newGrantsCmd(),
		newRequestsCmd(),
		newAskCmd(),
		newShareCmd(),
		newCastCmd(),
	)
	return cmd
}

func newPathListCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "ls [address]",
		Short: "What a machine serves",
		Long: "ls asks a machine what it serves and shows what you may reach.\n\n" +
			"What comes back is filtered by that machine: a path shared with someone else is\n" +
			"absent rather than refused, so this is what you have, not what exists.\n\n" +
			"An address with a path narrows the list to what is at or under it. What is inside\n" +
			"a directory somebody serves is a different question, and `drop file ls` asks it.\n\n" +
			"With no address, or one that is this machine, it lists what this machine serves —\n" +
			"unfiltered, because you are not a guest here.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return showOwnTable(reading())
			}

			at, err := ns.ParseAddress(args[0])
			if err != nil {
				return err
			}
			if at.Here {
				return showOwnTable(reading())
			}

			entry, err := resolve(at)
			if err != nil {
				return err
			}
			return listThere(cmd.Context(), at, entry, wait)
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 30*time.Second, "how long to spend reaching the machine")

	return cmd
}
