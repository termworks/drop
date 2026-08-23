package cmd

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/spf13/cobra"
)

func newIDCmd() *cobra.Command {
	var short bool

	cmd := &cobra.Command{
		Use:   "id",
		Short: "Print this node's identity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := node.LocalID()
			if err != nil {
				return err
			}
			if short {
				fmt.Println(node.Brief(id))
				return nil
			}
			fmt.Println(id)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&short, "short", "s", false, "print the abbreviated form")

	return cmd
}
