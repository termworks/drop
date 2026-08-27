package cmd

import (
	"fmt"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/plate"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/user"
)

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Who this machine belongs to",
		Long: "A device has an identity of its own, which is what the transport proves. A user is who\n" +
			"owns it, which is what an access rule names. The user key signs a badge for each machine,\n" +
			"and the badge is what says the two go together.\n\n" +
			"With no key configured, drop keeps one. $DROP_USER_KEY names another — a private key, or\n" +
			"the public half of one held by an ssh agent, which is how a key inside a YubiKey signs\n" +
			"without ever being read.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return showUser()
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "badge",
		Short: "Print this machine's badge and the signature over it",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			badge, sig, err := user.Mine(time.Now())
			if err != nil {
				return err
			}

			fmt.Print(string(badge.Bytes()))
			fmt.Print(string(sig))
			return nil
		},
	})

	return cmd
}

func showUser() error {
	where, err := user.Where()
	if err != nil {
		return err
	}

	pub, err := user.Public()
	if err != nil {
		return err
	}

	fmt.Printf("  key      %s\n", where)
	fmt.Printf("  identity %s", user.Text(pub))
	fmt.Printf("  as       %s\n\n", user.Fingerprint(pub))

	badge, _, err := user.Mine(time.Now())
	if err != nil {
		return fmt.Errorf("this machine has no badge: %w", err)
	}

	fmt.Printf("  this machine is %q, until %s\n",
		badge.Name, badge.Until.UTC().Format("2006-01-02"))

	return nil
}

// wearBadge picks up this machine's badge, so everything it opens says whose machine it is.
//
// Done once, here, rather than at every place a node starts: a node that forgot would arrive as a
// stranger for no reason anybody could see. Identity is not optional, so failing to get a badge is
// an error and not a quieter kind of node — the key is generated on first run if there is none, and
// what is left after that is a real failure worth saying out loud.
func wearBadge() error {
	badge, signed, err := user.Mine(time.Now())
	if err != nil {
		return err
	}
	proto.Carry(badge.Bytes(), signed)

	mine.Lock()
	mine.key = user.Text(badge.User)
	mine.Unlock()

	showPlate()
	carryHandover()
	return nil
}

// mine is this machine's own user key, kept because it is looked at on every connection and
// reading it from disk each time would be a file read per caller.
var mine struct {
	sync.Mutex
	key string
}

// myKey is the user key this machine belongs to, and empty when it has none.
func myKey() string {
	mine.Lock()
	defer mine.Unlock()

	return mine.key
}

// showPlate picks up this machine's plate, so everything it opens says what it is running on.
//
// Unlike a badge this is allowed to fail quietly. A machine that will not say what it is still has
// an identity and still works, and what is lost is only that nobody learns two of its accounts are
// on one machine — worth less than refusing to start over.
func showPlate() {
	stamp, signed, err := plate.Sign(time.Now())
	if err != nil {
		return
	}
	proto.Stamped(stamp.Bytes(), signed)
}
