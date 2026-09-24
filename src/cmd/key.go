package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/tui"
	"github.com/bresilla/drop/src/pkg/user"
)

// Your key: the one thing that says who you are, and the only thing that lets a machine become
// yours.
//
// drop makes one the first time it runs, so it works with nothing set up — but a key somebody never
// chose is a key they cannot see, and a machine joining "with no key" is what that looks like. So
// the key is shown, what it is and where it lives, and pointing drop at an SSH key you already have
// or at a YubiKey is one command.

// keySays is what the user key is, in a sentence: its fingerprint, and where it signs from.
func keySays() string {
	text := myKey()
	if text == "" {
		return "none: this machine wears no badge"
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(text))
	if err != nil {
		return "unreadable"
	}
	fp := user.Fingerprint(pub)
	where, err := user.Where()
	if err != nil {
		return fp
	}
	raw, err := keep.ReadFile(where, keep.MaxState)
	private := err == nil && strings.Contains(string(raw), "PRIVATE KEY")
	switch {
	case user.CanAssert():
		return fp + "  a YubiKey, held to this device when it signs"
	case isHardware(pub) && user.Named():
		return fp + "  a YubiKey, " + where
	case user.Named() && private:
		return fp + "  your SSH key, " + where
	case user.Named():
		return fp + "  held by ssh-agent, " + where
	case private:
		return fp + "  a key drop made itself, " + where
	}
	return fp + "  on another machine of yours, which signed this one's badge"
}

// isHardware reports whether a key lives in a security key.
func isHardware(pub ssh.PublicKey) bool {
	return pub.Type() == ssh.KeyAlgoSKED25519 || pub.Type() == ssh.KeyAlgoSKECDSA256
}

func newKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Your key: who you are, and what lets a machine become yours",
		Long: "Every machine of yours carries a badge this key signed, and only a machine that holds\n" +
			"it — or has the YubiKey it lives in — can make another one yours.\n\n" +
			"  drop me key use ~/.ssh/id_ed25519       an SSH key you already have\n" +
			"  drop me key use ~/.ssh/id_ed25519_sk.pub a key in a YubiKey",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			fmt.Printf("  your key  %s\n", keySays())
			if svc := ringingWith(); svc != nil && svc.Ringing() {
				fmt.Println("            looking for your other machines that hold it")
			}
			var others []tui.KeyChoice
			for _, k := range keyChoices() {
				if !k.Current {
					others = append(others, k)
				}
			}
			if len(others) > 0 {
				fmt.Println("\n  keys you could be instead:")
				for _, k := range others {
					fmt.Printf("    %-9s %s  %s\n", k.Kind, k.Print, tildePath(k.Path))
					if k.Note != "" {
						fmt.Printf("              %s\n", k.Note)
					}
				}
			}
			fmt.Println("\n  drop me key use <file> --yes    be one of them")
			fmt.Println("  drop me key yubikey             be the key in your YubiKey")
			return nil
		},
	}

	var fresh bool
	yubikey := &cobra.Command{
		Use:   "yubikey",
		Short: "Be the key in your YubiKey: its handles fetched, and it signs from then on",
		Long: "Takes the handles of the keys your YubiKey holds into ~/.ssh — its PIN, and a touch — and\n" +
			"becomes the one made for drop, or the only one. --new makes a key for drop on it first.\n\n" +
			"Every machine of yours that fetches the same key from the YubiKey finds the others by itself.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pubs, err := fetchYubiKey(fresh)
			if err != nil {
				return err
			}
			at, ok := pickFetched(pubs)
			if !ok {
				fmt.Println("your YubiKey holds more than one key:")
				for _, p := range pubs {
					fmt.Printf("  drop me key use %s --yes\n", tildePath(p))
				}
				return nil
			}
			pub, err := keyAt(at)
			if err != nil {
				return err
			}
			fmt.Println("signing this machine's badge — touch the YubiKey once more")
			if _, err := becomeKey(at, pub); err != nil {
				return err
			}
			fmt.Printf("you are %s now, the key in your YubiKey\n  %s\n", user.Fingerprint(pub), tildePath(at))
			if said := rekeyDaemon(cmd.Context()); said != "" {
				fmt.Println("  " + said)
			}
			return nil
		},
	}
	yubikey.Flags().BoolVar(&fresh, "new", false, "make a key for drop on the YubiKey first")
	cmd.AddCommand(yubikey)

	var sure bool
	use := &cobra.Command{
		Use:   "use <file>",
		Short: "Be the SSH key at a file, or the YubiKey whose .pub it is",
		Long: "A private key drop can read signs with no touch. The .pub of a key made with\n" +
			"`ssh-keygen -t ed25519-sk -O resident -O application=ssh:drop` is a YubiKey: it signs when\n" +
			"the key is there, and on a phone it is held to the phone.\n\n" +
			"This is a new you: every machine of yours is added again under it, with\n" +
			"`drop machine add` on each. It asks nothing: pass --yes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			at, err := filepath.Abs(expandHome(args[0]))
			if err != nil {
				return err
			}
			pub, err := keyAt(at)
			if err != nil {
				return err
			}
			if !sure && myKey() != user.Text(pub) {
				return fmt.Errorf("%s is %s: being it is a new you, and every machine of yours is added again; run this again with --yes", at, user.Fingerprint(pub))
			}
			changed, err := becomeKey(at, pub)
			if err != nil {
				return err
			}
			if !changed {
				fmt.Printf("you are %s, and drop reads it from %s now\n", user.Fingerprint(pub), at)
				return nil
			}
			fmt.Printf("you are %s now\n  %s\n\n", user.Fingerprint(pub), at)
			fmt.Println("  every other machine of yours that uses this key finds this one within a")
			fmt.Println("  minute or two, and it them. One without it takes a code: `drop machine add` here.")
			if said := rekeyDaemon(cmd.Context()); said != "" {
				fmt.Println("  " + said)
			}
			return nil
		},
	}
	use.Flags().BoolVar(&sure, "yes", false, "really be this key")
	cmd.AddCommand(use)
	return cmd
}

// keyAt reads the public half of the key a file names: a private key, or the .pub of one held in
// hardware or by an agent.
func keyAt(at string) (ssh.PublicKey, error) {
	raw, err := keep.ReadFile(at, keep.MaxState)
	if err != nil {
		return nil, err
	}
	if signer, err := ssh.ParsePrivateKey(raw); err == nil {
		return signer.PublicKey(), nil
	} else if errors.As(err, new(*ssh.PassphraseMissingError)) {
		return nil, fmt.Errorf("%s is locked with a passphrase, and drop signs when nobody is there to type it: use its .pub with ssh-agent holding it, or a key without one", at)
	}
	if pub, _, _, _, err := ssh.ParseAuthorizedKey(raw); err == nil {
		return pub, nil
	}
	return nil, fmt.Errorf("%s is not an SSH key", at)
}

// becomeKey makes the key at a file the user key: this machine's badge signed with it, then the
// config naming it. It says whether that changed who this machine belongs to.
func becomeKey(at string, pub ssh.PublicKey) (bool, error) {
	if os.Getenv("DROP_USER_KEY") != "" {
		return false, errors.New("$DROP_USER_KEY names the key here, and wins over the config: change that instead")
	}
	same := myKey() == user.Text(pub)
	was := ""
	if user.Named() {
		was, _ = user.Where()
	}
	user.Use(at)
	if !same {
		// Signed before the config names it, so a key that cannot sign is never left named, and a
		// YubiKey asks for its touch where somebody is looking.
		badge, signed, err := user.Mine(time.Now())
		if err != nil {
			user.Use(was)
			return false, fmt.Errorf("signing this machine's badge with %s: %w", at, err)
		}
		wear(badge, signed)
	}
	if err := writeUserKey(at); err != nil {
		return false, err
	}
	nudgeDoorbell()
	return !same, nil
}

// userKeyLine is the config line that names the user key.
var userKeyLine = regexp.MustCompile(`(?m)^\s*drop\.user_key\s*=.*$`)

// writeUserKey says in the config which key is the user key.
func writeUserKey(at string) error {
	file, err := conf.FilePath()
	if err != nil {
		return err
	}
	line := fmt.Sprintf("drop.user_key = %q", at)
	raw, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
		raw = []byte("local drop = require(\"drop\")\n\n" + line + "\n")
	case err != nil:
		return err
	case userKeyLine.Match(raw):
		raw = userKeyLine.ReplaceAll(raw, []byte(line))
	default:
		text := string(raw)
		if at := strings.Index(text, "require(\"drop\")"); at >= 0 {
			end := strings.Index(text[at:], "\n")
			if end < 0 {
				text += "\n"
				end = len(text) - at - 1
			}
			cut := at + end + 1
			text = text[:cut] + line + "\n" + text[cut:]
		} else {
			text = "local drop = require(\"drop\")\n" + line + "\n" + text
		}
		raw = []byte(text)
	}
	return keep.Replace(file, raw)
}

// expandHome is a path with ~ at its front made absolute.
func expandHome(at string) string {
	if !strings.HasPrefix(at, "~") {
		return at
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return at
	}
	return filepath.Join(home, strings.TrimPrefix(at, "~"))
}

// UseKey makes the key at a file who this user is, from an interface.
func (l *running) UseKey(at string) (string, error) {
	full, err := filepath.Abs(expandHome(strings.TrimSpace(at)))
	if err != nil {
		return "", err
	}
	pub, err := keyAt(full)
	if err != nil {
		return "", err
	}
	changed, err := becomeKey(full, pub)
	if err != nil {
		return "", err
	}
	if changed && l.daemon {
		_ = rekeyDaemon(context.Background())
	}
	return user.Fingerprint(pub), nil
}

// rekeyDaemon has the running drop take up the key the config names now, and says so; nothing when
// no drop is running, which reads the config when it starts.
func rekeyDaemon(ctx context.Context) string {
	said, err := atDaemon(ctx, "rekey")
	switch {
	case errors.Is(err, errNoDaemon):
		return ""
	case err != nil:
		return "the running drop did not answer: restart it, so it takes up the key"
	case strings.HasPrefix(said, "failed "):
		return "the running drop could not take up the key: " + strings.TrimPrefix(said, "failed ")
	}
	return "the running drop took it up"
}

// Keys is every key on this machine its user could be.
func (l *running) Keys() []tui.KeyChoice { return keyChoices() }

// Rekey takes up the key the config names now, here and in the daemon this is a view onto.
func (l *running) Rekey() error {
	if err := conf.ApplySettings(reading()); err != nil {
		return err
	}
	badge, signed, err := user.Mine(time.Now())
	if err != nil && !errors.Is(err, user.ErrStale) {
		return err
	}
	wear(badge, signed)
	nudgeDoorbell()
	if l.daemon {
		_ = rekeyDaemon(context.Background())
	}
	return nil
}
