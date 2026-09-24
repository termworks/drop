package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Your own machines: adding one is one command on each, and the rest of them hear about it by
// themselves.
//
// One machine shows a code and the other types it, the way a phone is linked to a messenger. Taking
// the code pairs the two and makes the new one yours in the same exchange, with a badge the showing
// machine signs for it, so there is no second code to carry and no vouching to do by hand.

func newMineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "machine",
		Short: "Machines: yours — add one, list them, rename or remove one",
		Long: "On a machine that is already yours run `drop machine add`, and on the new one\n" +
			"`drop machine join <code>` — or scan the code with drop on a phone. The new machine\n" +
			"becomes yours, and every other machine of yours learns of it within a few minutes.\n\n" +
			"Pairing with somebody else's machine is `drop peer pair`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return listMine() },
	}

	cmd.AddCommand(newMineAddCmd(), newMineJoinCmd(), newRenewCmd(), &cobra.Command{
		Use:   "ls",
		Short: "Every machine of yours this one knows",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return listMine() },
	}, &cobra.Command{
		Use:   "rm <name>",
		Short: "Take a machine out of yours, on every one of them",
		Long: "The machine is forgotten here and marked as taken out, and your other machines take the\n" +
			"mark from this one within a few minutes: from then on every one of them turns it away as a\n" +
			"stranger, whatever badge it still wears. `drop machine add` puts it back.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := forgetMine(args[0]); err != nil {
				return err
			}
			fmt.Printf("%s is no longer one of your machines; the rest of them hear within a few minutes\n", args[0])
			return nil
		},
	}, &cobra.Command{
		Use:   "rename <name> <new>",
		Short: "Call one of your machines something else here",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			if _, err := mineNamed(args[0]); err != nil {
				return err
			}
			return renameKnown(args[0], args[1])
		},
	})
	return cmd
}

// mineNamed is the machine of this user's filed under a name.
func mineNamed(name string) (book.Entry, error) {
	pinned, err := book.Load()
	if err != nil {
		return book.Entry{}, err
	}
	entry, ok := pinned.Lookup(name)
	if !ok {
		return book.Entry{}, fmt.Errorf("no machine here is called %q: `drop machine ls` lists yours", name)
	}
	if entry.User == "" || entry.User != myKey() {
		return book.Entry{}, fmt.Errorf("%s is not one of your machines: `drop peer forget %s` forgets it", name, name)
	}
	return entry, nil
}

// forgetMine takes one of this user's machines out of theirs.
func forgetMine(name string) error {
	if _, err := mineNamed(name); err != nil {
		return err
	}
	return forgetKnown(name, false)
}

func newMineAddCmd() *cobra.Command {
	var (
		as   string
		code string
		wait time.Duration
		key  bool
	)

	cmd := &cobra.Command{
		Use:   "add [code|name]",
		Short: "Add a machine of yours: show a code, take one, or ask a device you know",
		Long: "On a machine that holds your key, `drop machine add` shows a code and a QR. On the new\n" +
			"machine, `drop machine add <code>` takes it, or scan it with drop on a phone. The new\n" +
			"machine is given a badge your key signs, and every machine of yours learns of it.\n\n" +
			"With the name of a device on this network, or of somebody's machine you added, it asks\n" +
			"that device to become yours, and its person says yes on its screen.\n\n" +
			"--key hands the new machine the key itself instead, so it signs for itself and for others.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				given := strings.Join(args, " ")
				if near, ok := nearbyNamed(cmd.Context(), given); ok {
					return askNearby(cmd.Context(), near, proto.InviteMine)
				}
				if pinned, err := book.Load(); err == nil {
					if _, known := pinned.Lookup(given); known {
						return askAdded(cmd.Context(), given, proto.InviteMine)
					}
				}
				return joinPairing(cmd.Context(), given, as, joinWithin, offerAny, nil)
			}
			kind := offerMine
			if key {
				kind = offerMineKey
			}
			return offerPairing(cmd.Context(), as, code, wait, kind)
		},
	}

	cmd.Flags().StringVar(&as, "as", "", "the name to file the new machine under")
	cmd.Flags().StringVar(&code, "code", "", "use this code instead of a generated one")
	cmd.Flags().DurationVarP(&wait, "wait", "w", 5*time.Minute, "how long to keep the code up")
	cmd.Flags().BoolVar(&key, "key", false, "hand the new machine the user key itself")
	return cmd
}

func newMineJoinCmd() *cobra.Command {
	var (
		as string
		at []string
	)

	cmd := &cobra.Command{
		Use:   "join <code>",
		Short: "Become one of the machines of whoever is showing a code",
		Long: "The code is what `drop machine add` shows on a machine of yours. A ticket or a\n" +
			"drop://machine/ link works too.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return joinPairing(cmd.Context(), strings.Join(args, " "), as, joinWithin, offerMine, at)
		},
	}

	cmd.Flags().StringVar(&as, "as", "", "the name to file the machine showing the code under")
	cmd.Flags().StringSliceVar(&at, "at", nil, "where to reach the other machine, when finding it fails (host:port)")
	return cmd
}

// listMine prints this machine and every other of its user's in the book.
func listMine() error {
	pinned, err := book.Load()
	if err != nil {
		return err
	}
	id, err := node.LocalID()
	if err != nil {
		return err
	}

	me := myKey()
	var mine []book.Entry
	for _, e := range pinned.All() {
		if me != "" && e.User == me {
			mine = append(mine, e)
		}
	}

	width := len(node.DisplayName())
	for _, e := range mine {
		width = max(width, len(e.Name))
	}
	fmt.Printf("  %-*s  %-8s  %s\n", width, node.DisplayName(), "this one", id)
	for _, e := range mine {
		state := "known"
		if e.Paired() {
			state = "paired"
		}
		fmt.Printf("  %-*s  %-8s  %s\n", width, e.Name, state, e.ID)
	}
	if len(mine) == 0 {
		fmt.Printf("\nno other machine of yours yet: run `drop machine add` here, and\n`drop machine join <code>` on the other one.\n")
	}
	return nil
}
