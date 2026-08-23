// Package node holds this machine's identity and the iroh endpoint it speaks through.
package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/go-iroh/key"
)

// ID is what a device is addressed by: its ed25519 public key. It never changes.
type ID = key.EndpointID

// ParseID reads an id as it is written down.
func ParseID(text string) (ID, error) {
	return key.ParseEndpointID(text)
}

// ConfigDir is $XDG_CONFIG_HOME/drop, or ~/.config/drop.
func ConfigDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "drop"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".config", "drop"), nil
}

// Identity loads this node's secret key, generating and persisting one on first run.
//
// Stored as the raw ed25519 seed, which is what iroh's key package takes. A file that is not
// exactly a seed is refused rather than silently replaced: regenerating would change this device's
// address and quietly break every pairing it has.
func Identity() (key.SecretKey, error) {
	var empty key.SecretKey

	dir, err := ConfigDir()
	if err != nil {
		return empty, err
	}
	path := filepath.Join(dir, "identity")

	stored, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(stored) != key.SeedSize {
			return empty, fmt.Errorf(
				"%s is %d bytes, not a %d-byte key: it is probably from the libp2p build.\n"+
					"Move it aside to start fresh; this device's address will change and pairings must be redone",
				path, len(stored), key.SeedSize)
		}
		var seed [key.SeedSize]byte
		copy(seed[:], stored)
		return key.NewSecretKey(seed), nil

	case !errors.Is(err, os.ErrNotExist):
		return empty, fmt.Errorf("reading %s: %w", path, err)
	}

	fresh, err := key.GenerateSecretKey()
	if err != nil {
		return empty, fmt.Errorf("generating identity: %w", err)
	}
	seed := fresh.Bytes()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return empty, fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.WriteFile(path, seed[:], 0o600); err != nil {
		return empty, fmt.Errorf("writing %s: %w", path, err)
	}
	return fresh, nil
}

// LocalID is the address derived from the stored identity.
func LocalID() (ID, error) {
	sk, err := Identity()
	if err != nil {
		return ID{}, err
	}
	return sk.Public().EndpointID(), nil
}

// Brief is the abbreviated id used in listings and prompts.
func Brief(id ID) string {
	text := id.String()
	if len(text) <= 12 {
		return text
	}
	return text[len(text)-12:]
}
