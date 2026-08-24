package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/conf"
)

// Set once by Execute, read by the subcommands.
var (
	exiter  func(int) = os.Exit
	version string
)

func Execute(v string, exit func(int), args []string) {
	version = v
	exiter = exit

	root := &cobra.Command{
		Use:   "drop",
		Short: "Distributed peer-to-peer file transfer, streams and chat",
		Long: "One identity per device, and named namespaces under it. What a namespace does is\n" +
			"declared in the config, so opening one is the same command whatever it turns out to be.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,

		// With nothing to do, drop shows you your devices. Every other way in is a subcommand, and
		// the interface is what you want often enough that it should not need naming.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd.Context())
		},
	}

	// Settings before any command runs, so one that only dials still knows what it is allowed
	// to do. The commands that serve load the whole config themselves.
	root.PersistentPreRun = func(*cobra.Command, []string) { conf.ApplySettings() }

	root.SetArgs(args)
	root.AddCommand(
		newToCmd(),
		newServeCmd(),
		newCastCmd(),
		newPairCmd(),
		newPeersCmd(),
		newLogCmd(),
		newChatCmd(),
		newIDCmd(),
		newNamespacesCmd(),
		newPasswdCmd(),
		newListCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "drop:", err)
		exiter(1)
	}
}
