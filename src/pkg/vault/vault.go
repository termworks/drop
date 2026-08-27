// Package vault holds the key that everything on this disk is encrypted with.
//
// What is kept on disk is in the clear today: a conversation is a file of length-prefixed records,
// and `strings` reads it. Access rules do not help -- they decide what another *device* may reach
// over the wire, and this is about a disk that is no longer in your hands.
//
// So: envelope encryption, which is what age does internally and what this borrows. One data key,
// thirty-two random bytes, encrypts everything. The data key itself is written once, encrypted to
// age recipients, and unwrapped once at startup. A touch per message would be unusable; a touch per
// start is not.
//
// What this can do is protect a machine that is off -- a stolen laptop, a pulled disk, a leaked
// backup. What it cannot do is protect a machine that is running: drop has to read the data to show
// it to you, so anything with your session can ask drop. That is worth saying plainly rather than
// designing around.
package vault

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/bresilla/drop/src/pkg/keep"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"

	"github.com/bresilla/drop/src/pkg/node"
)

// KeyBytes is the width of the data key: a XChaCha20-Poly1305 key.
const KeyBytes = 32

// ErrLocked is a vault whose data key cannot be unwrapped -- the hardware is unplugged, or the key
// file is gone.
//
// It is a distinct error because a locked device is not an empty one. A peer asking for a path has
// to be told the device is locked rather than handed an empty answer that reads as the path being
// gone.
var ErrLocked = errors.New("this device is locked")

// Vault is the data key, once it has been unwrapped.
type Vault struct {
	key []byte
}

// Key is the data key. Empty for a vault that was never set up, which is a node keeping its history
// in the clear -- the default, and a decision rather than an oversight.
func (v *Vault) Key() []byte {
	if v == nil {
		return nil
	}
	return v.key
}

// On reports whether anything is encrypted at all.
func (v *Vault) On() bool { return v != nil && len(v.key) == KeyBytes }

// Where the wrapped data key lives.
func where() (string, error) {
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "keys", "data.age"), nil
}

// Open unwraps the data key, making one the first time.
//
// To is where the key is wrapped: age recipients, or a path to a key file. Nothing at all is a node
// that keeps its history in the clear, and it comes back with a vault holding no key rather than an
// error -- that is the default and it is not a failure.
func Open(to []string) (*Vault, error) {
	if len(to) == 0 {
		return &Vault{}, nil
	}

	file, err := where()
	if err != nil {
		return nil, err
	}

	wrapped, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return mint(file, to)
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	return unwrap(wrapped, to)
}

// mint generates a data key and wraps it to the recipients, once.
//
// Once is the whole of it. Two processes reaching this together would each make a key and each
// write it, and whichever landed second would own the file — while the other went on sealing
// records with a key that is nowhere. Those records are not recoverable by anybody, ever: there is
// no second copy of a key that was never written down. So the file is taken first and looked at
// again inside, because somebody may have made one while this was waiting.
func mint(file string, to []string) (*Vault, error) {
	var made *Vault

	err := keep.While(file, func() error {
		if wrapped, err := os.ReadFile(file); err == nil {
			got, err := unwrap(wrapped, to)
			if err != nil {
				return err
			}
			made = got
			return nil
		}

		key := [KeyBytes]byte{}
		if _, err := rand.Read(key[:]); err != nil {
			return fmt.Errorf("generating a data key: %w", err)
		}

		recipients, err := recipientsOf(to)
		if err != nil {
			return err
		}

		var wrapped bytes.Buffer
		writer, err := age.Encrypt(&wrapped, recipients...)
		if err != nil {
			return fmt.Errorf("wrapping the data key: %w", err)
		}
		if _, err := writer.Write(key[:]); err != nil {
			return fmt.Errorf("wrapping the data key: %w", err)
		}
		if err := writer.Close(); err != nil {
			return fmt.Errorf("wrapping the data key: %w", err)
		}

		if err := keep.Replace(file, wrapped.Bytes()); err != nil {
			return err
		}
		made = &Vault{key: key[:]}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return made, nil
}

// unwrap opens the wrapped data key with whichever identity is to hand.
func unwrap(wrapped []byte, to []string) (*Vault, error) {
	identities, err := identitiesOf(to)
	if err != nil {
		return nil, err
	}
	if len(identities) == 0 {
		return nil, ErrLocked
	}

	reader, err := age.Decrypt(bytes.NewReader(wrapped), identities...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLocked, err)
	}

	key, err := io.ReadAll(io.LimitReader(reader, KeyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLocked, err)
	}
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("the wrapped data key is %d bytes, expected %d", len(key), KeyBytes)
	}
	return &Vault{key: key}, nil
}

// recipientsOf turns what the config named into things a key can be wrapped to.
//
// Two forms, because they answer two different questions. An age recipient is a public key, and
// wrapping to it needs nothing present. A path is a key file, and wrapping to it needs the file --
// which is the honest version: a key beside the data it unlocks stops a resold disk and stops
// nothing on a machine somebody already has.
func recipientsOf(to []string) ([]age.Recipient, error) {
	var out []age.Recipient

	for _, at := range to {
		if strings.HasPrefix(at, "age1") {
			r, err := age.ParseX25519Recipient(at)
			if err != nil {
				return nil, fmt.Errorf("%s is not an age recipient: %w", at, err)
			}
			out = append(out, r)
			continue
		}

		identity, err := keyFile(at, true)
		if err != nil {
			return nil, err
		}
		out = append(out, identity.Recipient())
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("a vault needs somebody to wrap its key to")
	}
	return out, nil
}

// identitiesOf is the halves of those that can open what was wrapped.
//
// A recipient named in the config is a public key and cannot open anything; the private half is
// elsewhere, which is the point of naming it that way. So only key files come back here, and a
// vault wrapped to hardware alone is opened by a plugin, which is a piece of work of its own.
func identitiesOf(to []string) ([]age.Identity, error) {
	var out []age.Identity

	for _, at := range to {
		if strings.HasPrefix(at, "age1") {
			continue
		}
		identity, err := keyFile(at, false)
		if errors.Is(err, os.ErrNotExist) {
			// A key file that is not there is the locked case, not a broken config: the disk was
			// moved, or whatever holds the key is not mounted yet.
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, identity)
	}
	return out, nil
}

// keyFile reads an age key file, optionally making one that is not there yet.
func keyFile(at string, create bool) (*age.X25519Identity, error) {
	at = expand(at)

	raw, err := os.ReadFile(at)
	switch {
	case errors.Is(err, os.ErrNotExist) && create:
		return newKeyFile(at)
	case errors.Is(err, os.ErrNotExist):
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", at, err)
	}

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		identity, err := age.ParseX25519Identity(line)
		if err != nil {
			return nil, fmt.Errorf("%s does not hold an age key: %w", at, err)
		}
		return identity, nil
	}
	return nil, fmt.Errorf("%s holds no key", at)
}

// newKeyFile writes a key where the config said one should be.
func newKeyFile(at string) (*age.X25519Identity, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("generating an age key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(at), err)
	}
	body := fmt.Sprintf("# %s\n%s\n", identity.Recipient(), identity)
	if err := os.WriteFile(at, []byte(body), 0o600); err != nil {
		return nil, fmt.Errorf("writing %s: %w", at, err)
	}
	return identity, nil
}

// expand resolves ~ in a path, because a config is written by a person.
func expand(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// State is what a vault is doing, for a command that reports rather than acts.
type State int

const (
	// Off is a node with no vault: its history is in the clear, which is the default.
	Off State = iota
	// Fresh is a vault named in the config that has not been set up yet. Whatever is on disk now
	// is still in the clear.
	Fresh
	// Unlocked is a data key that unwrapped here.
	Unlocked
	// Locked is a data key that did not. What is on disk is there and unreadable.
	Locked
)

// Peek reports what a vault is doing without touching anything.
//
// Asking a question must not answer it by generating a key: `drop me vault` on a machine that has
// never run one would otherwise leave a key file behind and report a vault that did not exist a
// moment earlier.
func Peek(to []string) (State, error) {
	if len(to) == 0 {
		return Off, nil
	}

	file, err := where()
	if err != nil {
		return Off, err
	}
	wrapped, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Fresh, nil
	case err != nil:
		return Off, fmt.Errorf("reading %s: %w", file, err)
	}

	if _, err := unwrap(wrapped, to); errors.Is(err, ErrLocked) {
		return Locked, nil
	} else if err != nil {
		return Off, err
	}
	return Unlocked, nil
}
