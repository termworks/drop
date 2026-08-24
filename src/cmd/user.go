package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

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
