package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
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
	}

	root.SetArgs(args)
	root.AddCommand(
		newToCmd(),
		newServeCmd(),
		newPairCmd(),
		newPeersCmd(),
		newLogCmd(),
		newChatCmd(),
		newIDCmd(),
		newNamespacesCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "drop:", err)
		exiter(1)
	}
}
