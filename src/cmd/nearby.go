package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/tui"
)

// The devices on this network, and connecting to one of them by asking rather than by a code.

func newNearbyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nearby",
		Short: "Devices on this network, and connecting to one without a code",
		Long: "Lists every drop on this network nobody here has connected with yet, and every device\n" +
			"asking this one to connect. Asking one sends it a request its person says yes or no\n" +
			"to, with the same number on both screens:\n\n" +
			"  drop nearby mine <name>   make it one of your machines\n" +
			"  drop nearby pair <name>   pair with whoever owns it\n" +
			"  drop nearby join <name>   make this machine one of theirs\n\n" +
			"  drop nearby yes <name>    say yes to one asking this machine\n" +
			"  drop nearby no <name>     or no",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return listNearby(cmd.Context()) },
	}

	for _, one := range []struct{ use, short, kind string }{
		{"mine <name>", "Ask a device nearby to become one of your machines", proto.InviteMine},
		{"pair <name>", "Ask a device nearby to pair with you", proto.InvitePair},
		{"join <name>", "Ask a device nearby to take this machine into theirs", proto.InviteJoin},
	} {
		kind := one.kind
		cmd.AddCommand(&cobra.Command{
			Use:   one.use,
			Short: one.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return inviteNearby(cmd.Context(), args[0], kind)
			},
		})
	}
	for _, one := range []struct {
		use, short string
		yes        bool
	}{
		{"yes <name>", "Say yes to a device asking to connect", true},
		{"no <name>", "Say no to a device asking to connect", false},
	} {
		yes := one.yes
		cmd.AddCommand(&cobra.Command{
			Use:   one.use,
			Short: one.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return decideNearby(cmd.Context(), args[0], yes)
			},
		})
	}
	return cmd
}

// errNeedsDaemon is a nearby command with nothing holding this machine's address to do it.
var errNeedsDaemon = errors.New("nothing is running here to reach the network: start `drop serve`, or open `drop`")

func nearbyNow(ctx context.Context) ([]tui.Near, []tui.Invited, error) {
	said, err := atDaemon(ctx, "nearby")
	if errors.Is(err, errNoDaemon) {
		return nil, nil, errNeedsDaemon
	}
	if err != nil {
		return nil, nil, err
	}
	var near []tui.Near
	if err := json.Unmarshal([]byte(said), &near); err != nil {
		return nil, nil, err
	}
	said, err = atDaemon(ctx, "invited")
	if err != nil {
		return nil, nil, err
	}
	var asking []tui.Invited
	return near, asking, json.Unmarshal([]byte(said), &asking)
}

func listNearby(ctx context.Context) error {
	near, asking, err := nearbyNow(ctx)
	if err != nil {
		return err
	}
	for _, a := range asking {
		fmt.Printf("  %s asks %s — check %s\n    drop nearby yes %s\n\n", a.Name, invitedSays(a.Kind), a.Check, a.Name)
	}
	if len(near) == 0 {
		fmt.Println("nothing on this network that is not already yours or paired")
		return nil
	}
	for _, n := range near {
		fmt.Printf("  %-20s %s\n", n.Name, n.ID)
	}
	fmt.Println("\n  drop nearby mine <name>   make one of them yours")
	fmt.Println("  drop nearby pair <name>   pair with whoever owns it")
	return nil
}

// invitedSays is what an ask asks of this machine, as a sentence ends.
func invitedSays(kind string) string {
	switch kind {
	case proto.InviteMine:
		return "to make this machine one of theirs"
	case proto.InviteJoin:
		return "to become one of your machines"
	}
	return "to pair with you"
}

func inviteNearby(ctx context.Context, name, kind string) error {
	near, _, err := nearbyNow(ctx)
	if err != nil {
		return err
	}
	var to *tui.Near
	for i, n := range near {
		if strings.EqualFold(n.Name, name) || strings.HasPrefix(n.ID, name) {
			to = &near[i]
			break
		}
	}
	if to == nil {
		return fmt.Errorf("nothing nearby is called %q: `drop nearby` lists what is", name)
	}
	self, err := checkWith(to.ID)
	if err == nil {
		fmt.Printf("asking %s — on its screen, check that the number is %s\n", to.Name, self)
	}
	said, err := atDaemon(ctx, "invite "+kind+" "+to.ID)
	if err != nil {
		return err
	}
	fmt.Printf("connected with %s\n", strings.TrimPrefix(said, "paired "))
	return nil
}

func decideNearby(ctx context.Context, name string, yes bool) error {
	_, asking, err := nearbyNow(ctx)
	if err != nil {
		return err
	}
	for _, a := range asking {
		if strings.EqualFold(a.Name, name) || strings.HasPrefix(a.ID, name) {
			answer := "no"
			if yes {
				answer = "yes"
			}
			_, err := atDaemon(ctx, "decide "+a.ID+" "+answer)
			return err
		}
	}
	return fmt.Errorf("nothing called %q is asking: `drop nearby` lists who is", name)
}

// checkWith is the number this machine's screen shows for an ask to or from one device.
func checkWith(id string) (string, error) {
	self, err := node.LocalID()
	if err != nil {
		return "", err
	}
	to, err := node.ParseID(id)
	if err != nil {
		return "", err
	}
	return proto.Check(self, to), nil
}
