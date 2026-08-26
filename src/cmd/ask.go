package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/asked"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/grant"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

func newAskCmd() *cobra.Command {
	var why string

	cmd := &cobra.Command{
		Use:   "ask <address>",
		Short: "Ask to be let into a path you can see but cannot open",
		Long: "A path can be visible without being shared: it appears in a listing, marked locked,\n" +
			"and opening it is refused. This rings the bell on it.\n\n" +
			"  drop path ask orin:/vault --why \"for the thing we discussed\"\n\n" +
			"Nothing is granted by asking. The request is written down on the other machine for\n" +
			"somebody to look at, and they decide.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return askFor(cmd.Context(), args[0], why)
		},
	}

	cmd.Flags().StringVar(&why, "why", "", "a line about what it is for")
	return cmd
}

func askFor(parent context.Context, target, why string) error {
	at, err := ns.ParseAddress(target)
	if err != nil {
		return err
	}
	if at.Path == ns.Root {
		return fmt.Errorf("say which path: drop path ask <machine>:/<path>")
	}

	entry, err := resolve(at)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, askFor_wait)
	defer cancel()

	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	lan, _ := discovery.StartLAN(ctx, n)

	done, s, err := best(n, lan).To(ctx, entry, node.ALPNSession)
	if err != nil {
		return err
	}
	defer done.Close()
	defer s.Close()

	if err := proto.Ask(ctx, s, at.Path, why, node.DisplayName()); err != nil {
		return err
	}

	fmt.Printf("asked %s for %s\n", entry.Name, at.Path)
	fmt.Printf("nothing is granted by asking: somebody there decides.\n")
	return nil
}

// askFor_wait bounds one request, which is a dial and a sentence.
const askFor_wait = 90 * time.Second

func newRequestsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "requests",
		Aliases: []string{"asked"},
		Short:   "Who has asked to be let into a path",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return showRequests()
		},
	}

	cmd.AddCommand(newRequestsAllowCmd(), newRequestsRefuseCmd())
	return cmd
}

func showRequests() error {
	all, err := asked.All()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		fmt.Println("nobody has asked for anything")
		return nil
	}

	for _, one := range all {
		fmt.Printf("\n  %s\n", one.Path)
		fmt.Printf("    from  %s\n", one.Who())
		fmt.Printf("    id    %s\n", node.Brief(one.From))
		if one.Why != "" {
			fmt.Printf("    why   %s\n", one.Why)
		}
		fmt.Printf("    when  %s\n", one.At.Local().Format("2 Jan 15:04"))
	}

	fmt.Printf("\n  drop path requests allow  <path> <who>\n")
	fmt.Printf("  drop path requests refuse <path> <who>\n\n")
	return nil
}

func newRequestsAllowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "allow <path> <who>",
		Short: "Grant a request, and take it off the list",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := answering(args[0], args[1], true); err != nil {
				return err
			}
			fmt.Printf("%s may reach %s\n", args[1], args[0])
			return nil
		},
	}
}

func newRequestsRefuseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refuse <path> <who>",
		Short: "Turn a request down, and take it off the list",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := answering(args[0], args[1], false); err != nil {
				return err
			}
			fmt.Printf("%s is refused at %s\n", args[1], args[0])
			return nil
		},
	}
}

// answering is the shared half of allowing and refusing: change the grant, then drop every request
// for that path from whoever it was, because it has been dealt with either way.
func answering(path, who string, allow bool) error {
	store, err := grant.Load()
	if err != nil {
		return err
	}

	if allow {
		err = store.Allow(path, who)
	} else {
		err = store.Deny(path, who)
	}
	if err != nil {
		return err
	}

	all, err := asked.All()
	if err != nil {
		return err
	}
	for _, one := range all {
		if one.Path == path && one.Who() == who {
			if err := asked.Answered(one.From, one.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

// taking is what a serving node does with a request: write it down, and nothing else.
func taking() func(proto.Asker) error {
	return func(who proto.Asker) error {
		return asked.Ring(asked.Request{
			From:   who.From,
			Name:   who.Name,
			Person: who.Person,
			Path:   who.Path,
			Why:    who.Why,
			At:     time.Now(),
		})
	}
}
