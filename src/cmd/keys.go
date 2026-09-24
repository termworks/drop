package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/tui"
	"github.com/bresilla/drop/src/pkg/user"
)

// Every key somebody could be, found rather than typed: the SSH keys they already have, the keys
// their YubiKey holds once its handles are fetched, and the one drop made.

// keyChoices is every key on this machine drop could sign as, the one in use first.
func keyChoices() []tui.KeyChoice {
	current := myKey()
	var out []tui.KeyChoice
	seen := map[string]bool{}
	add := func(at string, pub ssh.PublicKey, kind, note string) {
		text := user.Text(pub)
		if seen[text] {
			return
		}
		seen[text] = true
		out = append(out, tui.KeyChoice{Path: at, Print: user.Fingerprint(pub), Kind: kind, Note: note, Current: text == current})
	}

	if dir, err := node.ConfigDir(); err == nil {
		own := filepath.Join(dir, "user")
		if raw, err := keep.ReadFile(own, keep.MaxState); err == nil {
			if signer, err := ssh.ParsePrivateKey(raw); err == nil {
				add(own, signer.PublicKey(), "drop's own", "")
			}
		}
	}

	if home, err := os.UserHomeDir(); err == nil {
		pubs, _ := filepath.Glob(filepath.Join(home, ".ssh", "*.pub"))
		sort.Strings(pubs)
		for _, at := range pubs {
			raw, err := keep.ReadFile(at, keep.MaxState)
			if err != nil {
				continue
			}
			pub, _, _, _, err := ssh.ParseAuthorizedKey(raw)
			if err != nil {
				continue
			}
			held := strings.TrimSuffix(at, ".pub")
			if isHardware(pub) {
				note := ""
				if _, err := os.Stat(held); err != nil {
					note = "its handle is not on this machine: fetch it from the YubiKey"
				}
				add(at, pub, "YubiKey", note)
				continue
			}
			secret, err := keep.ReadFile(held, keep.MaxState)
			switch {
			case err != nil:
				add(at, pub, "SSH key", "only its public half is here: ssh-agent has to hold it")
			case strings.Contains(string(secret), "ENCRYPTED") || isLocked(secret):
				add(at, pub, "SSH key", "locked with a passphrase: ssh-agent has to hold it")
			case pub.Type() == ssh.KeyAlgoRSA:
				add(held, pub, "SSH key", "RSA: machines holding it are added with a code, not by themselves")
			default:
				add(held, pub, "SSH key", "")
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Current && !out[j].Current })
	return out
}

// isLocked reports whether a private key needs a passphrase to read.
func isLocked(raw []byte) bool {
	_, err := ssh.ParsePrivateKey(raw)
	return errors.As(err, new(*ssh.PassphraseMissingError))
}

// fetchYubiKey takes the handles of every key a YubiKey holds into ~/.ssh, asking for its PIN and
// a touch in this terminal, and says which keys it now has there. With fresh, it makes a key for
// drop on the YubiKey first.
func fetchYubiKey(fresh bool) ([]string, error) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return nil, errors.New("ssh-keygen is not installed, and it is what talks to a YubiKey")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, err
	}

	if fresh {
		at := freePath(filepath.Join(sshDir, "id_ed25519_sk_drop"))
		fmt.Println("making a key for drop on your YubiKey — touch it when it blinks")
		if err := interactive("ssh-keygen", "-t", "ed25519-sk", "-O", "resident", "-O", "application=ssh:drop", "-N", "", "-C", "drop", "-f", at); err != nil {
			return nil, fmt.Errorf("the YubiKey made no key: %w", err)
		}
		return []string{at + ".pub"}, nil
	}

	staging, err := os.MkdirTemp(sshDir, ".drop-fetch-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	fmt.Println("taking the keys from your YubiKey — its PIN, then a touch")
	cmd := exec.Command("ssh-keygen", "-K")
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = staging, os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("no keys came off the YubiKey: %w", err)
	}

	var out []string
	pubs, _ := filepath.Glob(filepath.Join(staging, "*.pub"))
	for _, pub := range pubs {
		held := strings.TrimSuffix(pub, ".pub")
		name := filepath.Base(held)
		to := filepath.Join(sshDir, name)
		if same(held, to) {
			out = append(out, to+".pub")
			continue
		}
		to = freePath(to)
		if err := os.Rename(held, to); err != nil {
			return out, err
		}
		if err := os.Rename(pub, to+".pub"); err != nil {
			return out, err
		}
		_ = os.Chmod(to, 0o600)
		out = append(out, to+".pub")
	}
	if len(out) == 0 {
		return nil, errors.New("your YubiKey holds no key drop can use yet: `drop me key yubikey --new` makes one on it")
	}
	return out, nil
}

// pickFetched is the key to be, of those a YubiKey gave: the one made for drop, or the only one.
func pickFetched(pubs []string) (string, bool) {
	for _, at := range pubs {
		if strings.HasSuffix(strings.TrimSuffix(at, ".pub"), "_rk_drop") || strings.HasSuffix(strings.TrimSuffix(at, ".pub"), "_sk_drop") {
			return at, true
		}
	}
	if len(pubs) == 1 {
		return pubs[0], true
	}
	return "", false
}

// same reports whether two files hold the same bytes.
func same(a, b string) bool {
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && string(x) == string(y)
}

// freePath is a path nothing is at: the one given, or it with a number after.
func freePath(at string) string {
	out := at
	for i := 2; ; i++ {
		if _, err := os.Stat(out); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Stat(out + ".pub"); errors.Is(err, os.ErrNotExist) {
				return out
			}
		}
		out = fmt.Sprintf("%s-%d", at, i)
	}
}

// interactive runs a command on this terminal.
func interactive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// tildePath is a path as a person would type it, with their home written as ~.
func tildePath(at string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(at, home+"/") {
		return "~" + strings.TrimPrefix(at, home)
	}
	return at
}
