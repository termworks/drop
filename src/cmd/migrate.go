package cmd

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/metal"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/plate"
)

// Moving to another machine.
//
// A name taken from the hardware stays with the hardware, so replacing a machine is not something
// that can quietly happen — it has to be said, by the old machine, while it is still running. What
// comes out is one line to carry across; what goes in is that line.

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate <new-machine-id>",
		Short: "Say this machine became another one",
		Long: "Signs a statement that this machine has become another, with the key every machine\n" +
			"paired with this one already knows it by. Run it here, while this machine still\n" +
			"works, and give what it prints to the new machine.\n\n" +
			"Get the new machine's id from `drop me id` on that machine.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			to, err := node.ParseID(strings.TrimSpace(args[0]))
			if err != nil {
				return fmt.Errorf("%q is not a machine id: %w", args[0], err)
			}

			over, sig, err := plate.Hand(to, metal.Whose(), time.Now())
			if err != nil {
				return err
			}

			fmt.Printf("%s is now %s\n\n", node.Brief(over.Was), node.Brief(over.Now))
			fmt.Printf("%s\n\n", packed(over.Bytes(), sig))
			fmt.Printf("give that to the new machine, and run there:\n")
			fmt.Printf("  drop me machine took <that line>\n\n")
			fmt.Printf("it is good until %s, and moves this account only.\n",
				over.Until.UTC().Format("2006-01-02 15:04"))
			return nil
		},
	}
	return cmd
}

func newTookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "took <handover>",
		Short: "Read a handover a machine signed, and say what it moves",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			signed, sig, err := unpacked(strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}

			over, err := plate.Took(signed, sig, time.Now())
			if err != nil {
				return err
			}

			here, err := node.LocalID()
			if err != nil {
				return err
			}
			if over.Now != here {
				return fmt.Errorf("that handover points at %s, and this machine is %s",
					node.Brief(over.Now), node.Brief(here))
			}

			fmt.Printf("  %s said it became this machine\n", node.Brief(over.Was))
			fmt.Printf("  account   %s\n", over.Whose)
			fmt.Printf("  good till %s\n\n", over.Until.UTC().Format("2006-01-02 15:04"))
			fmt.Printf("  this checks out. Show it to whoever knew the old machine — each of them\n")
			fmt.Printf("  decides for themselves, because each of them holds the old name.\n")
			return nil
		},
	}
	return cmd
}

// packed is a signed statement as one line somebody can paste: what was signed and the signature,
// with a length in front so the two come apart again.
func packed(signed, sig []byte) string {
	out := make([]byte, 0, 4+len(signed)+len(sig))
	out = append(out, byte(len(signed)>>8), byte(len(signed)))
	out = append(out, signed...)
	out = append(out, sig...)
	return "drop1" + base64.RawURLEncoding.EncodeToString(out)
}

func unpacked(text string) (signed, sig []byte, err error) {
	rest, found := strings.CutPrefix(text, "drop1")
	if !found {
		return nil, nil, fmt.Errorf("that is not something drop wrote")
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return nil, nil, fmt.Errorf("that handover is not readable: %w", err)
	}
	if len(raw) < 2 {
		return nil, nil, fmt.Errorf("that handover is too short to be one")
	}

	n := int(raw[0])<<8 | int(raw[1])
	if n > len(raw)-2 {
		return nil, nil, fmt.Errorf("that handover says it is longer than it is")
	}
	return raw[2 : 2+n], raw[2+n:], nil
}
