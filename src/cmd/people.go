package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/tui"
)

// People, their machines, and the topics on each: the three things drop is about, and the same
// three words on the command line as on every screen.
//
//	drop person add            show your code, for somebody to take
//	drop person add <code>     take the code somebody shows
//	drop machine add           show a code a new machine of yours takes
//	drop topic add work folder put a folder called work on this machine

func newPersonCmd() *cobra.Command {
	var (
		as   string
		wait time.Duration
		at   []string
	)
	cmd := &cobra.Command{
		Use:   "person",
		Short: "People: you, and everybody you added",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return listPeople() },
	}

	add := &cobra.Command{
		Use:   "add [code|name]",
		Short: "Add somebody: show your code, take theirs, or ask a device on this network",
		Long: "With nothing after it, shows a code and a QR for the other person to take. With the code\n" +
			"they show, takes it. With the name of a device on this network (`drop nearby`), asks it,\n" +
			"and its person says yes on their screen.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return offerPairing(cmd.Context(), as, "", wait, offerPerson)
			}
			given := strings.Join(args, " ")
			if near, ok := nearbyNamed(cmd.Context(), given); ok {
				return askNearby(cmd.Context(), near, proto.InvitePair)
			}
			return joinPairing(cmd.Context(), given, as, wait, offerAny, at)
		},
	}
	add.Flags().StringVar(&as, "as", "", "what to call them here")
	add.Flags().DurationVarP(&wait, "wait", "w", 5*time.Minute, "how long to keep the code up")
	add.Flags().StringSliceVar(&at, "at", nil, "where to reach the other device, when finding it fails (host:port)")

	var undo bool
	trust := &cobra.Command{
		Use:   "trust <name>",
		Short: "Trust somebody, so topics open to people you trust open to them",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := trustKnown(args[0], !undo); err != nil {
				return err
			}
			if undo {
				fmt.Printf("%s is no longer trusted\n", args[0])
			} else {
				fmt.Printf("%s is trusted\n", args[0])
			}
			return nil
		},
	}
	trust.Flags().BoolVar(&undo, "undo", false, "stop trusting them instead")

	cmd.AddCommand(add, trust, &cobra.Command{
		Use:   "ls",
		Short: "Everybody, with their machines",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return listPeople() },
	}, &cobra.Command{
		Use:   "rename <name> <new>",
		Short: "Call somebody something else, on every machine of yours",
		Args:  cobra.ExactArgs(2),
		RunE:  func(_ *cobra.Command, args []string) error { return renameKnown(args[0], args[1]) },
	}, &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove somebody and every machine of theirs, on every machine of yours",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := forgetKnown(args[0], true); err != nil {
				return err
			}
			fmt.Printf("removed %s\n", args[0])
			return nil
		},
	})
	return cmd
}

// nearbyNamed is the device on this network a name or the start of an id means, when the daemon
// hears one.
func nearbyNamed(ctx context.Context, name string) (tui.Near, bool) {
	near, _, err := nearbyNow(ctx)
	if err != nil {
		return tui.Near{}, false
	}
	for _, n := range near {
		if strings.EqualFold(n.Name, name) || (len(name) >= 6 && strings.HasPrefix(n.ID, name)) {
			return n, true
		}
	}
	return tui.Near{}, false
}

// askNearby asks a device on this network something, and waits for its person.
func askNearby(ctx context.Context, near tui.Near, kind string) error {
	if check, err := checkWith(near.ID); err == nil {
		fmt.Printf("asking %s — on its screen, check that the number is %s\n", near.Name, check)
	}
	said, err := atDaemon(ctx, "invite "+kind+" "+near.ID)
	if err != nil {
		return err
	}
	fmt.Printf("done: %s\n", strings.TrimPrefix(said, "paired "))
	return nil
}

// trustKnown trusts somebody, or stops, and every machine of theirs with them.
func trustKnown(name string, trusted bool) error {
	pinned, err := book.Load()
	if err != nil {
		return err
	}
	return pinned.Change(func() (bool, error) {
		entries, _, err := managedEntries(pinned, name, true)
		if err != nil {
			return false, err
		}
		if len(entries) == 0 {
			return false, fmt.Errorf("nobody here is called %q", name)
		}
		pinned.Trust(entries[0].Name, trusted)
		return true, nil
	})
}

// listPeople prints you and everybody else, each with their machines.
func listPeople() error {
	pinned, err := book.Load()
	if err != nil {
		return err
	}
	byPerson := map[string][]string{}
	trusted := map[string]bool{}
	me := myKey()
	for _, e := range pinned.All() {
		who := e.Person
		switch {
		case me != "" && e.User == me:
			who = "me"
		case e.User == "":
			who = e.Name + " (on its own)"
		}
		byPerson[who] = append(byPerson[who], e.Name)
		trusted[who] = trusted[who] || e.Trusted
	}
	byPerson["me"] = append([]string{node.DisplayName() + " (this one)"}, byPerson["me"]...)

	names := make([]string, 0, len(byPerson))
	for who := range byPerson {
		if who != "me" {
			names = append(names, who)
		}
	}
	sort.Strings(names)
	for _, who := range append([]string{"me"}, names...) {
		mark := ""
		if trusted[who] && who != "me" {
			mark = "  trusted"
		}
		fmt.Printf("  %s%s\n", who, mark)
		for _, m := range byPerson[who] {
			fmt.Printf("      %s\n", m)
		}
	}
	if len(names) == 0 {
		fmt.Println("\nnobody else yet: `drop person add` shows your code")
	}
	return nil
}

func newTopicCmd() *cobra.Command {
	var on, level, command string
	cmd := &cobra.Command{
		Use:   "topic",
		Short: "Topics: what each machine offers — chat, folder, inbox, note, terminal",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return showOwnTable(reading()) },
	}

	add := &cobra.Command{
		Use:   "add <name> <kind>",
		Short: "Add a topic to this machine, or to another of yours with --on",
		Long: "The kind is one of " + kindList() + ". A folder, inbox or note is kept under\n" +
			"~/drop on the machine it is added to; a stream needs --command. Who may open it is --for:\n" +
			"me (the default), trusted, paired or anyone.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := json.Marshal(proto.TopicBody{Kind: args[1], Command: command})
			if err != nil {
				return err
			}
			at := "/" + strings.Trim(args[0], "/")
			if _, err := askOn(cmd.Context(), on, proto.Manage{Op: proto.ManageAdd, Path: at, Level: level, Body: string(body)}); err != nil {
				return err
			}
			fmt.Printf("%s is a %s on %s now\n", at, args[1], machineOr(on))
			return nil
		},
	}
	add.Flags().StringVar(&on, "on", "", "which machine of yours to add it to; this one by default")
	add.Flags().StringVar(&level, "for", "me", "who may open it: me, trusted, paired or anyone")
	add.Flags().StringVar(&command, "command", "", "the command a stream shows")

	var rmOn string
	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Take a topic away",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			at := "/" + strings.Trim(args[0], "/")
			if _, err := askOn(cmd.Context(), rmOn, proto.Manage{Op: proto.ManageRemove, Path: at}); err != nil {
				return err
			}
			fmt.Printf("%s is gone from %s\n", at, machineOr(rmOn))
			return nil
		},
	}
	rm.Flags().StringVar(&rmOn, "on", "", "which machine of yours it is on; this one by default")

	var whoOn string
	who := &cobra.Command{
		Use:   "who <name> [me|trusted|paired|anyone]",
		Short: "Who may open a topic, or put it on another step",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			m := proto.Manage{Op: proto.ManageRead, Path: "/" + strings.Trim(args[0], "/")}
			if len(args) == 2 {
				m.Op, m.Level = proto.ManageLevel, args[1]
			}
			raw, err := askOn(cmd.Context(), whoOn, m)
			if err != nil {
				return err
			}
			var d tui.PathDetail
			if err := json.Unmarshal(raw, &d); err != nil {
				return err
			}
			fmt.Printf("%s on %s opens for: %s\n", d.Path, machineOr(whoOn), levelSays(d.Level))
			return nil
		},
	}
	who.Flags().StringVar(&whoOn, "on", "", "which machine of yours it is on; this one by default")

	cmd.AddCommand(add, rm, who, &cobra.Command{
		Use:   "ls [machine]",
		Short: "The topics on a machine: this one, one of yours, or somebody else's",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return showOwnTable(reading())
			}
			at, err := ns.ParseAddress(args[0])
			if err != nil {
				return err
			}
			if at.Here {
				return showOwnTable(reading())
			}
			entry, err := resolve(cmd.Context(), at)
			if err != nil {
				return err
			}
			return listThere(cmd.Context(), at, entry, topicsWithin)
		},
	}, &cobra.Command{
		Use:   "kinds",
		Short: "Every kind a topic can be",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			for _, k := range made.Kinds {
				fmt.Printf("  %-10s %s\n", k.Name, k.About)
			}
			return nil
		},
	})
	return cmd
}

// topicsWithin is how long a machine is waited on for what topics it has.
const topicsWithin = 30 * time.Second

func machineOr(name string) string {
	if name == "" {
		return "this machine"
	}
	return name
}

func levelSays(level string) string {
	switch level {
	case ns.LevelMe:
		return "only you"
	case "trusted":
		return "you, and the people you trust"
	case "paired":
		return "everybody you added"
	case "anyone":
		return "anyone at all"
	}
	return "what its config says"
}

// askOn asks one of this user's machines about a topic — this one when the name is empty — through
// the daemon, which holds the connections.
func askOn(ctx context.Context, machine string, m proto.Manage) ([]byte, error) {
	if machine == "" || machine == node.DisplayName() {
		if m.Op == proto.ManageAdd || m.Op == proto.ManageRemove {
			return manageTopic(ctx, reading(), nil, m)
		}
		return ManageHere(reading(), m)
	}
	entry, err := mineNamed(machine)
	if err != nil {
		return nil, err
	}
	s, err := viaDaemon(ctx, entry, node.ALPNManage)
	if errors.Is(err, errNoDaemon) {
		return nil, errNeedsDaemon
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Done() }()
	return proto.AskManage(s, m)
}
