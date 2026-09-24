package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
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
		Short: "Your own machines: add one, join one, list them",
		Long: "On a machine that is already yours run `drop machine add`, and on the new one\n" +
			"`drop machine join <code>` — or scan the code with drop on a phone. The new machine\n" +
			"becomes yours, and every other machine of yours learns of it within a few minutes.\n\n" +
			"Pairing with somebody else's machine is `drop peer pair`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return listMine() },
	}

	cmd.AddCommand(newMineAddCmd(), newMineJoinCmd(), &cobra.Command{
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

func newTakeCodeCmd() *cobra.Command {
	var (
		as string
		at []string
	)
	cmd := &cobra.Command{
		Use:   "join <code>",
		Short: "Take a code another device is showing, whatever it is for",
		Long: "A code from `drop machine add` makes this machine one of that user's; one from `drop peer\n" +
			"pair` pairs it with whoever showed it. The code says which, so this is the one command for\n" +
			"either. A device on this network is simpler still: `drop nearby`.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return joinPairing(cmd.Context(), strings.Join(args, " "), as, joinWithin, offerAny, at)
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "the name to file whoever showed the code under")
	cmd.Flags().StringSliceVar(&at, "at", nil, "where to reach the other device, when finding it fails (host:port)")
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
		Use:   "add",
		Short: "Show a code another machine joins you with",
		Long: "Shows a short code and a QR code. On the new machine run `drop machine join <code>`,\n" +
			"or scan it with drop on a phone. The new machine is given a badge signed here, which\n" +
			"any machine of yours renews before it runs out.\n\n" +
			"--key hands it the user key itself instead, so it signs for itself and for others.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
