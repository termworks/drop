package cmd

import (
	"github.com/spf13/cobra"
)

// This machine and the person it belongs to: what it is called on the wire, who signs for it, what
// it keeps on disk, and what has been said through it.

func newMeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "me",
		Short: "This machine, and who it belongs to",
		Long: "A machine has an identity of its own, which is what the transport proves. A user is\n" +
			"who owns it, which is what an access rule names. The two are separate on purpose,\n" +
			"and this group is where each of them is read back.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	cmd.AddCommand(
		newIDCmd(),
		newMachineCmd(),
		newUserCmd(),
		newVaultCmd(),
		newPasswdCmd(),
		newLogCmd(),
	)
	return cmd
}
