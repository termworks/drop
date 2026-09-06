package user

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
)

// The user key is who somebody is, as against which machine they are sitting at.
//
// An ssh key, because signing is what it is for, because most people already have one, and because
// it is the one kind of key that covers a file on a server and a key inside a YubiKey without drop
// knowing the difference. What signs is an ssh.Signer; where it came from is somebody's business
// and not this program's.

// Namespace keeps drop's signatures to themselves: one made here cannot be replayed as an ssh
// login or a git commit signature, and neither of those can become a badge.
const Namespace = "drop"

// chosen is the key the config named, if it named one.
//
// Set before anything asks, because settings are applied before a badge is worn. A package-level
// value rather than an argument threaded through every caller: there is exactly one user key per
// running drop, and a caller that forgot to pass it would quietly sign as somebody else.
var chosen struct {
	sync.RWMutex
	at string
}

// Use names the key drop should sign with, from the config. $DROP_USER_KEY still wins, so a profile
// or a one-off command can point somewhere else without editing anything.
func Use(at string) {
	chosen.Lock()
	defer chosen.Unlock()

	chosen.at = at
}

// Where is the user key drop will use.
//
// Three places, narrowest first. $DROP_USER_KEY is for a profile or a one-off. `drop.user_key` in
// the config is where somebody says, once, that their identity is the SSH key they already have.
// Failing both, drop keeps a key of its own — identity is not optional, so something has to exist.
//
// Any of them may name a private key file, or the public half of one held by an agent, which is how
// a YubiKey takes part without its key ever being read.
func Where() (string, error) {
	if named := os.Getenv("DROP_USER_KEY"); named != "" {
		return named, nil
	}

	chosen.RLock()
	named := chosen.at
	chosen.RUnlock()

	if named != "" {
		return named, nil
	}

	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "user"), nil
}

// Named reports whether the key was pointed at rather than made up by drop. A key drop generated is
// a fallback, and worth saying so when somebody asks what their identity is.
func Named() bool {
	if os.Getenv("DROP_USER_KEY") != "" {
		return true
	}

	chosen.RLock()
	defer chosen.RUnlock()

	return chosen.at != ""
}

// Signer is the key this user signs with, making one if there is none.
//
// Identity is not optional — an access rule that names a person is meaningless without it — but it
// must not require hardware: a machine with nobody near it has to come back after a reboot at four
// in the morning. So a key is generated when none is configured, and a YubiKey is an upgrade
// somebody chooses rather than a thing they must own.
func Signer() (ssh.Signer, error) {
	where, err := Where()
	if err != nil {
		return nil, err
	}

	raw, err := keep.ReadFile(where, keep.MaxState)
	if errors.Is(err, os.ErrNotExist) {
		// A key that was pointed at and is not there is a mistake worth reporting. Generating one
		// at that path would answer a typo by inventing a second identity.
		if Named() {
			return nil, fmt.Errorf("no key at %s", where)
		}
		return makeKey(where)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", where, err)
	}

	// A private key drop can use directly.
	if signer, err := ssh.ParsePrivateKey(raw); err == nil {
		return signer, nil
	}

	// Otherwise it is a public key, and whoever holds the private half is an agent. This is the
	// hardware case: the key is in somebody's pocket and only ever leaves a signature.
	pub, _, _, _, err := ssh.ParseAuthorizedKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is neither a private key nor a public one", where)
	}
	return fromAgent(pub)
}

// Public is this user's identity, without needing whatever holds the private half.
//
// Reading it must not wake a YubiKey: knowing who you are is not an operation anybody should have
// to touch a key for.
func Public() (ssh.PublicKey, error) {
	where, err := Where()
	if err != nil {
		return nil, err
	}

	raw, err := keep.ReadFile(where, keep.MaxState)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !Named() {
			signer, err := makeKey(where)
			if err != nil {
				return nil, err
			}
			return signer.PublicKey(), nil
		}
		return nil, fmt.Errorf("reading %s: %w", where, err)
	}

	if pub, _, _, _, err := ssh.ParseAuthorizedKey(raw); err == nil {
		return pub, nil
	}
	if signer, err := ssh.ParsePrivateKey(raw); err == nil {
		return signer.PublicKey(), nil
	}

	// A public half kept beside the private one, which is what ssh-keygen writes.
	if beside, err := keep.ReadFile(where+".pub", keep.MaxState); err == nil {
		if pub, _, _, _, err := ssh.ParseAuthorizedKey(beside); err == nil {
			return pub, nil
		}
	}
	return nil, fmt.Errorf("cannot read a public key from %s", where)
}

// makeKey writes a new user key.
func makeKey(where string) (ssh.Signer, error) {
	var signer ssh.Signer
	err := keep.While(where, func() error {
		raw, err := keep.ReadFile(where, keep.MaxState)
		switch {
		case err == nil:
			signer, err = ssh.ParsePrivateKey(raw)
			if err != nil {
				return fmt.Errorf("%s is not a private key", where)
			}
			return nil
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("reading %s: %w", where, err)
		}

		pub, secret, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		block, err := ssh.MarshalPrivateKey(secret, "drop user key")
		if err != nil {
			return err
		}
		if err := keep.Replace(where, pem.EncodeToMemory(block)); err != nil {
			return err
		}
		signer, err = ssh.NewSignerFromKey(secret)
		if err != nil {
			return err
		}

		beside, err := ssh.NewPublicKey(pub)
		if err != nil {
			return err
		}
		_ = keep.Replace(where+".pub", ssh.MarshalAuthorizedKey(beside))
		return nil
	})
	return signer, err
}

// fromAgent finds a key in the running ssh-agent.
func fromAgent(want ssh.PublicKey) (ssh.Signer, error) {
	at := os.Getenv("SSH_AUTH_SOCK")
	if at == "" {
		return nil, errors.New("that key is held by an agent, and no agent is running")
	}
	return findAgent(want, at, agentListWithin)
}

func findAgent(want ssh.PublicKey, at string, within time.Duration) (ssh.Signer, error) {
	conn, err := dialAgent(at, within)
	if err != nil {
		return nil, fmt.Errorf("reaching the ssh agent: %w", err)
	}
	defer func() { _ = conn.Close() }()

	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		return nil, fmt.Errorf("asking the ssh agent: %w", err)
	}

	for _, signer := range signers {
		if string(signer.PublicKey().Marshal()) == string(want.Marshal()) {
			return agentSigner{key: want, socket: at, within: agentSignWithin}, nil
		}
	}
	return nil, fmt.Errorf("the ssh agent does not hold %s", Fingerprint(want))
}

type agentSigner struct {
	key    ssh.PublicKey
	socket string
	within time.Duration
}

func (s agentSigner) PublicKey() ssh.PublicKey { return s.key }

func (s agentSigner) Sign(_ io.Reader, data []byte) (*ssh.Signature, error) {
	within := s.within
	if within <= 0 {
		within = agentSignWithin
	}
	conn, err := dialAgent(s.socket, within)
	if err != nil {
		return nil, fmt.Errorf("reaching the ssh agent: %w", err)
	}
	defer func() { _ = conn.Close() }()
	return agent.NewClient(conn).Sign(s.key, data)
}

func dialAgent(at string, within time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()

	dialer := net.Dialer{Timeout: agentDialWithin}
	conn, err := dialer.DialContext(ctx, "unix", at)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

const (
	agentDialWithin = 2 * time.Second
	agentListWithin = 10 * time.Second
	agentSignWithin = 2 * time.Minute
)

// Fingerprint is a key as a person recognises it, which is how ssh prints one.
func Fingerprint(key ssh.PublicKey) string { return ssh.FingerprintSHA256(key) }

// Text is a key as it is written down: the one-line form authorized_keys uses.
func Text(key ssh.PublicKey) string {
	return string(ssh.MarshalAuthorizedKey(key))
}
