package user

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

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

// Where is the user key drop will use.
//
// $DROP_USER_KEY names a key to use — a private key file, or the public half of one held by an
// agent, which is how a YubiKey takes part without its key ever being read. Otherwise drop keeps
// one of its own.
func Where() (string, error) {
	if named := os.Getenv("DROP_USER_KEY"); named != "" {
		return named, nil
	}

	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "user"), nil
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

	raw, err := os.ReadFile(where)
	if errors.Is(err, os.ErrNotExist) {
		if os.Getenv("DROP_USER_KEY") != "" {
			return nil, fmt.Errorf("no key at %s", where)
		}
		return make(where)
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

	raw, err := os.ReadFile(where)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && os.Getenv("DROP_USER_KEY") == "" {
			signer, err := make(where)
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
	if beside, err := os.ReadFile(where + ".pub"); err == nil {
		if pub, _, _, _, err := ssh.ParseAuthorizedKey(beside); err == nil {
			return pub, nil
		}
	}
	return nil, fmt.Errorf("cannot read a public key from %s", where)
}

// make writes a new user key.
func make(where string) (ssh.Signer, error) {
	if err := os.MkdirAll(filepath.Dir(where), 0o700); err != nil {
		return nil, err
	}

	pub, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	block, err := ssh.MarshalPrivateKey(secret, "drop user key")
	if err != nil {
		return nil, err
	}

	// The same form ssh-keygen writes, so ssh-keygen can read it: somebody who wants to sign a
	// badge by hand, or move this key onto a YubiKey later, should not need drop to do it.
	if err := os.WriteFile(where, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("writing %s: %w", where, err)
	}

	signer, err := ssh.NewSignerFromKey(secret)
	if err != nil {
		return nil, err
	}

	beside, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(where+".pub", ssh.MarshalAuthorizedKey(beside), 0o644)

	return signer, nil
}

// fromAgent finds a key in the running ssh-agent.
func fromAgent(want ssh.PublicKey) (ssh.Signer, error) {
	at := os.Getenv("SSH_AUTH_SOCK")
	if at == "" {
		return nil, errors.New("that key is held by an agent, and no agent is running")
	}

	conn, err := net.Dial("unix", at)
	if err != nil {
		return nil, fmt.Errorf("reaching the ssh agent: %w", err)
	}

	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		return nil, fmt.Errorf("asking the ssh agent: %w", err)
	}

	for _, signer := range signers {
		if string(signer.PublicKey().Marshal()) == string(want.Marshal()) {
			return signer, nil
		}
	}
	return nil, fmt.Errorf("the ssh agent does not hold %s", Fingerprint(want))
}

// Fingerprint is a key as a person recognises it, which is how ssh prints one.
func Fingerprint(key ssh.PublicKey) string { return ssh.FingerprintSHA256(key) }

// Text is a key as it is written down: the one-line form authorized_keys uses.
func Text(key ssh.PublicKey) string {
	return string(ssh.MarshalAuthorizedKey(key))
}
