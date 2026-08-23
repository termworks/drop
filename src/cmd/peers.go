package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
)

func newPeersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peers",
		Short: "The devices this one knows",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := book.Load()
			if err != nil {
				return err
			}

			entries := b.All()
			if len(entries) == 0 {
				fmt.Println("nothing known yet: run `drop pair` to link a device")
				return nil
			}
			for _, e := range entries {
				state := "known"
				if e.Paired() {
					state = "paired"
				}
				fmt.Printf("  %-16s %-7s %s\n", e.Name, state, e.ID)
			}
			return nil
		},
	}

	cmd.AddCommand(newPeersRmCmd())

	return cmd
}

func newPeersRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Forget a device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := book.Load()
			if err != nil {
				return err
			}
			if !b.Remove(args[0]) {
				return fmt.Errorf("%q is not known", args[0])
			}
			if err := b.Save(); err != nil {
				return err
			}

			fmt.Printf("forgot %s\n", args[0])
			return nil
		},
	}
}
