package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/metal"
	"github.com/bresilla/drop/src/pkg/node"
)

// What this machine is called, and what would change it.
//
// Worth a command of its own because the answer is not obvious from anywhere else: a machine
// named by its hardware and one named by a file on its disk look identical until the disk is
// wiped, and that is the worst moment to find out which one you had.

func newMachineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "machine",
		Short: "What names this machine, and what would change it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := node.LocalID()
			if err != nil {
				return err
			}
			mark, err := node.Naming()
			if err != nil {
				return err
			}

			fmt.Printf("%s\n\n", id)

			at, err := node.Written()
			if err != nil {
				return err
			}
			switch {
			case mark.Held():
				fmt.Printf("  named by      %s\n", mark.Says)
				fmt.Printf("  which reads   %s\n", mark.Brief())
				fmt.Printf("  and by        where this drop keeps its things, so every account and\n")
				fmt.Printf("                profile here is reachable as itself\n")
				fmt.Printf("  checked by    the public identity in %s.hardware\n", at)
				fmt.Printf("  survives      a reinstall when the same hardware source is available\n")
				fmt.Printf("  refuses if    %s\n", changes(mark.From))
			default:
				fmt.Printf("  named by      the key kept in %s\n", at)
				fmt.Printf("  survives      a reinstall only if that file is in your backup\n")
				if now := metal.Read(); now.Held() {
					fmt.Printf("\n  this machine could name itself instead, by %s.\n", now.Says)
					fmt.Printf("  `drop me machine rebind` changes it over — and every pairing that\n")
					fmt.Printf("  names this machine has to be made again.\n")
				}
			}

			if metal.Sealing() {
				fmt.Printf("\n  it has a TPM drop can reach, so what it keeps can be sealed to it.\n")
			}
			return nil
		},
	}

	cmd.AddCommand(newRebindCmd(), newMigrateCmd(), newTookCmd())
	return cmd
}

// changes says what would give this machine another name, which is the one thing somebody needs to
// know before they rely on it having this one.
func changes(from metal.Source) string {
	switch from {
	case metal.Chip:
		return "the TPM is cleared, or this drop is moved to another home"
	case metal.Board:
		return "the board is replaced, or this drop is moved to another home"
	case metal.Disk:
		return "the drive the system is on is replaced, or this drop is moved to another home"
	}
	return "anything at all"
}

func newRebindCmd() *cobra.Command {
	var sure bool

	cmd := &cobra.Command{
		Use:   "rebind",
		Short: "Stop using the written-down key and be named by the hardware",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mark := metal.Read()
			if !mark.Held() {
				return fmt.Errorf("this machine says nothing about itself, so there is nothing to be named by")
			}

			at, err := node.Written()
			if err != nil {
				return err
			}
			if _, err := os.Stat(at); os.IsNotExist(err) {
				return fmt.Errorf("this machine is already named by %s", mark.Says)
			}
			if sure {
				rebound, err := node.RebindHardware()
				if err != nil {
					return err
				}
				fmt.Printf("this machine is now %s, named by %s\n", node.Brief(rebound.Now), rebound.From.Says)
				fmt.Printf("who it was is in %s\n", rebound.Kept)
				return nil
			}

			where, err := node.ConfigDir()
			if err != nil {
				return err
			}
			was, err := node.LocalID()
			if err != nil {
				return err
			}
			seed, err := mark.Seed(where)
			if err != nil {
				return err
			}
			becomes := node.From(seed)

			fmt.Printf("this machine is %s\n", node.Brief(was))
			fmt.Printf("it would become %s, named by %s\n\n", node.Brief(becomes), mark.Says)
			fmt.Printf("every machine paired with this one knows it by the old name and will not\n")
			fmt.Printf("recognise the new one. Each pairing has to be made again.\n\n")
			fmt.Printf("run it again with --yes to go ahead.\n")
			return nil
		},
	}

	cmd.Flags().BoolVar(&sure, "yes", false, "go ahead, knowing the pairings have to be made again")
	return cmd
}
