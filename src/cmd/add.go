package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Adding a device, and then deciding what it is to you.
//
// Every device starts the same way: added, which pairs the two, whoever owns it. What it is to you
// is a second step taken on a device you have added — make it one of your machines, or make this
// one of theirs — and its person says yes to it on their screen. So there is one way in, and the
// relationship is something changed afterwards, not something chosen before anybody has met.

func newAddCmd() *cobra.Command {
	var (
		as   string
		wait time.Duration
		at   []string
	)
	cmd := &cobra.Command{
		Use:   "add [code]",
		Short: "Add a device: show a code, or take the one it shows",
		Long: "On one device run `drop add`: it shows a code and a QR. On the other, `drop add <code>`, or\n" +
			"tap Add on a phone. The two are paired from then on.\n\n" +
			"What it is to you comes after: `drop promote <name>` makes it one of your machines, and\n" +
			"`drop join <name>` makes this machine one of theirs. Its person says yes on their screen.\n" +
			"A device on this network needs no code at all: `drop nearby`.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return joinPairing(cmd.Context(), strings.Join(args, " "), as, wait, offerAny, at)
			}
			return offerPairing(cmd.Context(), as, "", wait, offerPerson)
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "the name to file the other device under")
	cmd.Flags().DurationVarP(&wait, "wait", "w", 5*time.Minute, "how long to keep the code up")
	cmd.Flags().StringSliceVar(&at, "at", nil, "where to reach the other device, when finding it fails (host:port)")
	return cmd
}

func newPromoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote <name>",
		Short: "Make a device you added one of your machines",
		Long: "Asks the device, and its person says yes on its screen, with the same number on both.\n" +
			"It carries your identity from then on, and every other machine of yours learns of it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return askAdded(cmd.Context(), args[0], proto.InviteMine)
		},
	}
}

func newJoinThemCmd() *cobra.Command {
	var (
		as string
		at []string
	)
	cmd := &cobra.Command{
		Use:   "join <name|code>",
		Short: "Make this machine one of the machines of a device you added",
		Long: "Asks the device, and its person says yes on its screen, with the same number on both. This\n" +
			"machine carries their identity from then on.\n\n" +
			"Given a code instead of a name, it takes the code, as `drop add <code>` does.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			given := strings.Join(args, " ")
			if pinned, err := book.Load(); err == nil {
				if _, known := pinned.Lookup(given); known {
					return askAdded(cmd.Context(), given, proto.InviteJoin)
				}
			}
			return joinPairing(cmd.Context(), given, as, joinWithin, offerAny, at)
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "the name to file whoever showed the code under")
	cmd.Flags().StringSliceVar(&at, "at", nil, "where to reach the other device, when finding it fails (host:port)")
	return cmd
}

// askAdded asks a device already added to change what it is to this one, and waits for its person.
func askAdded(ctx context.Context, name, kind string) error {
	pinned, err := book.Load()
	if err != nil {
		return err
	}
	entry, known := pinned.Lookup(name)
	if !known {
		return fmt.Errorf("no device here is called %q: add it first, with `drop add`", name)
	}
	if entry.User != "" && entry.User == myKey() {
		return fmt.Errorf("%s is one of your machines already", name)
	}
	if check, err := checkWith(entry.ID.String()); err == nil {
		fmt.Printf("asking %s — on its screen, check that the number is %s\n", name, check)
	}
	said, err := atDaemon(ctx, "invite "+kind+" "+entry.ID.String())
	if errors.Is(err, errNoDaemon) {
		return errNeedsDaemon
	}
	if err != nil {
		return err
	}
	with := strings.TrimPrefix(said, "paired ")
	if kind == proto.InviteMine {
		fmt.Printf("%s is one of your machines now\n", with)
	} else {
		fmt.Printf("this machine is one of %s's now\n", with)
	}
	return nil
}
