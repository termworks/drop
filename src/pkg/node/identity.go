// Package node holds this machine's identity and the iroh endpoint it speaks through.
package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/metal"
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
		return profileDir(filepath.Join(dir, "drop"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return profileDir(filepath.Join(home, ".config", "drop"))
}

// Identity is this node's secret key: taken from the machine itself where the machine will say
// what it is, and kept in a file where it will not.
//
// A key written down is a key that dies with the disk and travels with a copy of it. A key derived
// from the hardware does neither: reinstall and it comes back, because the thing it was derived
// from never left; carry the backup elsewhere and it does not, because it was never in the backup.
//
// A file that is already there still wins. A machine that has been running with a written-down key
// has pairings that name it, and quietly deriving a different one would break every one of them on
// an ordinary upgrade. Changing over is deliberate, and `drop me rebind` is where that is said.
func Identity() (key.SecretKey, error) {
	sk, _, err := identity()
	return sk, err
}

// Naming is where this machine's identity came from, for a person asking what would change it.
func Naming() (metal.Mark, error) {
	_, from, err := identity()
	return from, err
}

// Written is where a machine's key is kept when it has to be kept.
func Written() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "identity"), nil
}

func identity() (key.SecretKey, metal.Mark, error) {
	var empty key.SecretKey

	path, err := Written()
	if err != nil {
		return empty, metal.Mark{}, err
	}

	stored, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(stored) != key.SeedSize {
			return empty, metal.Mark{}, fmt.Errorf(
				"%s is %d bytes, not a %d-byte key.\n"+
					"Move it aside to start fresh; this machine's address will change and pairings must be redone",
				path, len(stored), key.SeedSize)
		}
		var seed [key.SeedSize]byte
		copy(seed[:], stored)
		return key.NewSecretKey(seed), metal.Mark{}, nil

	case !errors.Is(err, os.ErrNotExist):
		return empty, metal.Mark{}, fmt.Errorf("reading %s: %w", path, err)
	}

	// Nothing written down: this is a machine that has not run before, or one that has been wiped
	// and is meant to come back as itself.
	if mark := metal.Read(); mark.Held() {
		seed, err := mark.Seed(filepath.Dir(path))
		if err != nil {
			return empty, metal.Mark{}, err
		}
		return key.NewSecretKey(seed), mark, nil
	}

	// Made exactly once, however many drops start at the same moment.
	//
	// Two processes reaching this together would each make a key and each write it, and the one
	// whose write landed second would win the file while the other went on running its whole
	// session — endpoint, badge, everything it signs — under a key that is not the one on disk.
	// Every pairing a peer recorded during that session would name an address that vanishes at the
	// next restart, with nothing to say so. `drop serve` and `drop peer pair` are separate
	// processes sharing this directory, so this is the ordinary way to start, not a rare one.
	var made key.SecretKey
	err = keep.While(path, func() error {
		// Somebody may have made one while this was waiting for the file.
		if raw, err := os.ReadFile(path); err == nil && len(raw) == key.SeedSize {
			var seed [key.SeedSize]byte
			copy(seed[:], raw)
			made = key.NewSecretKey(seed)
			return nil
		}

		fresh, err := key.GenerateSecretKey()
		if err != nil {
			return fmt.Errorf("generating identity: %w", err)
		}
		seed := fresh.Bytes()
		if err := keep.Replace(path, seed[:]); err != nil {
			return err
		}
		made = fresh
		return nil
	})
	if err != nil {
		return empty, metal.Mark{}, err
	}
	return made, metal.Mark{}, nil
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

// From is the id a seed would give, for saying what a machine would become before it becomes it.
func From(seed [32]byte) ID {
	return key.NewSecretKey(seed).Public().EndpointID()
}
