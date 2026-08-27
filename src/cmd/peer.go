package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
)

// The address book: which machines this one knows, whose they are, and what it thinks of them.

func newPeerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "The machines this one knows",
		Long: "Pairing is recognition: a machine arrives with a name instead of as a stranger.\n" +
			"Trust is the second, deliberate step, and it is what the narrower access rules are\n" +
			"written against.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	cmd.AddCommand(
		newPairCmd(),
		newPeerListCmd(),
		newPeerTrustCmd(),
		newPeerForgetCmd(),
		newPeerWhoisCmd(),
	)
	return cmd
}

func newPeerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "Every machine in the address book",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			pinned, err := book.Load()
			if err != nil {
				return err
			}

			entries := pinned.All()
			if len(entries) == 0 {
				fmt.Println("nothing known yet: run `drop peer pair` to link a machine")
				return nil
			}
			for _, e := range entries {
				state := "known"
				if e.Paired() {
					state = "paired"
				}
				if e.Trusted {
					state += ", trusted"
				}
				fmt.Printf("  %-16s %-16s %-12s %s\n", e.Name, state, e.Person, e.ID)
			}
			return nil
		},
	}
}

func newPeerTrustCmd() *cobra.Command {
	var undo bool

	cmd := &cobra.Command{
		Use:   "trust <name>",
		Short: "Say you would show this person things without thinking about it",
		Long: "Trust belongs to a person, not to one of their laptops: every machine of theirs\n" +
			"in the book is marked with them, and one paired later arrives already trusted.\n\n" +
			"Use --undo to take it back.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pinned, err := book.Load()
			if err != nil {
				return err
			}
			if _, known := pinned.Lookup(args[0]); !known {
				return fmt.Errorf("%q is not a machine this one knows", args[0])
			}

			pinned.Trust(args[0], !undo)
			if err := pinned.Save(); err != nil {
				return err
			}

			if undo {
				fmt.Printf("%s is no longer trusted\n", args[0])
				return nil
			}
			fmt.Printf("%s is trusted\n", args[0])
			return nil
		},
	}

	cmd.Flags().BoolVar(&undo, "undo", false, "stop trusting them instead")

	return cmd
}

func newPeerForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget <name>",
		Short: "Drop a machine from the address book",
		Long: "The shared secret goes with it, so the two are strangers again and would have to\n" +
			"pair to reach each other by name. Nobody else is told.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pinned, err := book.Load()
			if err != nil {
				return err
			}
			if !pinned.Remove(args[0]) {
				return fmt.Errorf("%q is not known", args[0])
			}
			if err := pinned.Save(); err != nil {
				return err
			}

			fmt.Printf("forgot %s\n", args[0])
			return nil
		},
	}
}

func newPeerWhoisCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whois <name|id>",
		Short: "What this machine knows about another",
		Long: "There are two ways a machine is recognised. It may be in the address book, which\n" +
			"is what pairing writes. Or it may carry a badge signed by somebody who is, which is\n" +
			"a machine of theirs this one has never met.\n\n" +
			"This is the first half, read back: who a rule naming them would be about.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			entry, err := book.Resolve(args[0])
			if err != nil {
				return err
			}

			pinned, err := book.Load()
			if err != nil {
				return err
			}
			who := whoIs(pinned)(entry.ID, proto.Badged{}, proto.Stood{})

			fmt.Printf("\n  %s  %s\n\n", entry.Name, node.Brief(entry.ID))
			fmt.Printf("  id       %s\n", entry.ID)
			fmt.Printf("  paired   %v\n", who.Paired)
			fmt.Printf("  trusted  %v\n", who.Trusted)

			if entry.Owned() {
				fmt.Printf("  person   %s\n", entry.Person)
				fmt.Printf("  user     %s\n", entry.User)
			} else {
				fmt.Printf("  person   nobody: this machine was paired on its own\n")
			}
			for _, at := range entry.Addrs {
				fmt.Printf("  seen at  %s\n", at)
			}
			fmt.Println()
			return nil
		},
	}
}
