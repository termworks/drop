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

const hardwareIdentitySize = key.PublicKeySize * 2

type hardwareIdentity struct {
	secret key.SecretKey
	from   metal.Mark
}

// Rebinding is the completed transition from a stored identity to the hardware identity.
type Rebinding struct {
	Was  ID
	Now  ID
	From metal.Mark
	Kept string
}

// RebindHardware retires the stored identity after pinning the hardware identity.
func RebindHardware() (Rebinding, error) {
	path, err := Written()
	if err != nil {
		return Rebinding{}, err
	}
	mark := metal.Read()
	if !mark.Held() {
		return Rebinding{}, fmt.Errorf("this machine says nothing about itself, so there is nothing to be named by")
	}
	seed, err := mark.Seed(filepath.Dir(path))
	if err != nil {
		return Rebinding{}, err
	}
	hardware := hardwareIdentity{secret: key.NewSecretKey(seed), from: mark}
	was, kept, err := rebindHardware(path, hardware)
	if err != nil {
		return Rebinding{}, err
	}
	return Rebinding{
		Was:  was,
		Now:  hardware.secret.Public().EndpointID(),
		From: mark,
		Kept: kept,
	}, nil
}

func identity() (key.SecretKey, metal.Mark, error) {
	var empty key.SecretKey

	path, err := Written()
	if err != nil {
		return empty, metal.Mark{}, err
	}

	stored, exists, err := readIdentity(path)
	if err != nil {
		return empty, metal.Mark{}, err
	}
	if exists {
		return stored, metal.Mark{}, nil
	}

	var hardware *hardwareIdentity
	if mark := metal.Read(); mark.Held() {
		seed, err := mark.Seed(filepath.Dir(path))
		if err != nil {
			return empty, metal.Mark{}, err
		}
		hardware = &hardwareIdentity{secret: key.NewSecretKey(seed), from: mark}
	}
	return chooseIdentity(path, hardware)
}

func chooseIdentity(path string, hardware *hardwareIdentity) (key.SecretKey, metal.Mark, error) {
	var selected key.SecretKey
	var from metal.Mark
	err := keep.While(path, func() error {
		stored, exists, err := readIdentity(path)
		if err != nil {
			return err
		}
		if exists {
			selected = stored
			return nil
		}

		anchor := path + ".hardware"
		if hardware != nil {
			id := hardware.secret.Public().EndpointID()
			if err := pinHardware(anchor, id); err != nil {
				return err
			}
			selected, from = hardware.secret, hardware.from
			return nil
		}

		anchored, exists, err := readHardware(anchor)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf(
				"hardware identity %s recorded in %s is unavailable; refusing to replace it",
				Brief(anchored), anchor)
		}

		fresh, err := key.GenerateSecretKey()
		if err != nil {
			return fmt.Errorf("generating identity: %w", err)
		}
		seed := fresh.Bytes()
		if err := keep.Replace(path, seed[:]); err != nil {
			return err
		}
		selected = fresh
		return nil
	})
	if err != nil {
		return key.SecretKey{}, metal.Mark{}, err
	}
	return selected, from, nil
}

func rebindHardware(path string, hardware hardwareIdentity) (ID, string, error) {
	var was ID
	kept := path + ".was"
	err := keep.While(path, func() error {
		stored, exists, err := readIdentity(path)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("this machine is already named by %s", hardware.from.Says)
		}
		if _, err := os.Lstat(kept); err == nil {
			return fmt.Errorf("%s already holds a recovery identity; move it aside before rebinding", kept)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("checking %s: %w", kept, err)
		}

		was = stored.Public().EndpointID()
		now := hardware.secret.Public().EndpointID()
		if err := keep.Replace(path+".hardware", []byte(now.String())); err != nil {
			return err
		}
		if err := keep.Rename(path, kept); err != nil {
			return fmt.Errorf("moving %s aside: %w", path, err)
		}
		return nil
	})
	return was, kept, err
}

func readIdentity(path string) (key.SecretKey, bool, error) {
	raw, err := keep.ReadFile(path, key.SeedSize)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return key.SecretKey{}, false, nil
	case err != nil:
		return key.SecretKey{}, false, fmt.Errorf("reading %s: %w", path, err)
	case len(raw) != key.SeedSize:
		return key.SecretKey{}, false, fmt.Errorf(
			"%s is %d bytes, not a %d-byte key.\n"+
				"Move it aside to start fresh; this machine's address will change and pairings must be redone",
			path, len(raw), key.SeedSize)
	}
	var seed [key.SeedSize]byte
	copy(seed[:], raw)
	return key.NewSecretKey(seed), true, nil
}

func readHardware(path string) (ID, bool, error) {
	raw, err := keep.ReadFile(path, hardwareIdentitySize)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ID{}, false, nil
	case err != nil:
		return ID{}, false, fmt.Errorf("reading %s: %w", path, err)
	case len(raw) != hardwareIdentitySize:
		return ID{}, false, fmt.Errorf(
			"%s is %d bytes, not a %d-byte endpoint id", path, len(raw), hardwareIdentitySize)
	}
	id, err := ParseID(string(raw))
	if err != nil {
		return ID{}, false, fmt.Errorf("reading %s: invalid endpoint id: %w", path, err)
	}
	return id, true, nil
}

func pinHardware(path string, current ID) error {
	anchored, exists, err := readHardware(path)
	if err != nil {
		return err
	}
	if exists {
		if anchored != current {
			return fmt.Errorf(
				"hardware now derives identity %s, but %s records %s; refusing to change identity",
				Brief(current), path, Brief(anchored))
		}
		return nil
	}
	return keep.Replace(path, []byte(current.String()))
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
