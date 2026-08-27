package user

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
)

// The badge this machine carries, kept beside the identity it is about.
//
// Signed once and read on every connection, so it lives on disk rather than being made each time:
// with a key in somebody's pocket, making one costs a touch.

// badgeAt is where this machine keeps its badge and the signature over it.
func badgeAt() (string, error) {
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "badge"), nil
}

// Mine is this machine's badge, signing one if there is none or if it has run out.
//
// The device it names is this machine's own identity, so a badge cannot be moved to another
// machine: the transport proves which machine is calling, and a badge for a different one says
// nothing about it.
func Mine(now time.Time) (Badge, []byte, error) {
	where, err := badgeAt()
	if err != nil {
		return Badge{}, nil, err
	}

	signed, err := os.ReadFile(where)
	sig, sigErr := os.ReadFile(where + ".sig")

	if err == nil && sigErr == nil {
		badge, err := Read(signed, sig, now)
		if err == nil && badge.Device == deviceID() && sameUser(badge) {
			return badge, sig, nil
		}
		// Anything wrong with it — run out, for another machine, signed by a key this user no
		// longer has — is a reason to make a new one rather than to fail.
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Badge{}, nil, fmt.Errorf("reading %s: %w", where, err)
	}

	return renew(where, now)
}

// renew signs a fresh badge for this machine and writes it down.
func renew(where string, now time.Time) (Badge, []byte, error) {
	badge, sig, err := mineNow(now)
	if err != nil {
		return Badge{}, nil, err
	}

	if err := os.MkdirAll(filepath.Dir(where), 0o700); err != nil {
		return Badge{}, nil, err
	}
	if err := os.WriteFile(where, badge.Bytes(), 0o644); err != nil {
		return Badge{}, nil, fmt.Errorf("writing %s: %w", where, err)
	}
	if err := os.WriteFile(where+".sig", sig, 0o644); err != nil {
		return Badge{}, nil, fmt.Errorf("writing %s: %w", where+".sig", err)
	}
	return badge, sig, nil
}

// sameUser reports whether a badge was signed by the key this machine now uses.
func sameUser(badge Badge) bool {
	mine, err := Public()
	if err != nil {
		return false
	}
	return string(mine.Marshal()) == string(badge.User.Marshal())
}

// deviceID is this machine's own identity, read without starting anything.
// mineNow makes this machine's badge, whichever way the key can be reached.
//
// A key drop can read is signed here and now. A key it cannot -- one in hardware, or held by an
// agent -- is signed by the command that can reach it, which is the config's to name and
// `ssh-keygen -Y sign` by default.
func mineNow(now time.Time) (Badge, []byte, error) {
	where, err := Where()
	if err != nil {
		return Badge{}, nil, err
	}

	if command := signCommand(where); command != "" {
		who, err := Public()
		if err != nil {
			return Badge{}, nil, err
		}
		return SignBy(command, who, deviceID(), node.DisplayName(), now)
	}

	by, err := Signer()
	if err != nil {
		return Badge{}, nil, err
	}
	return Sign(by, deviceID(), node.DisplayName(), now)
}

func deviceID() string {
	id, err := node.LocalID()
	if err != nil {
		return ""
	}
	return id.String()
}
