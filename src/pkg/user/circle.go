package user

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/crypto/hkdf"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
)

// The circle is every machine of one user's, and its secret is what lets any two of them find
// each other without having paired.
//
// Two machines of yours already know each other when they meet — the badge says so. What a machine
// needs before they meet is a way to find the other, and finding is done under a secret only the
// two of them can work out. Pairing makes one per pair; the circle makes one for every pair at
// once, from a secret all of your machines hold and hand to each other when they say hello.

// CircleSize is how long the circle's secret is.
const CircleSize = 32

func circleAt() (string, error) {
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "circle"), nil
}

// Circle is this machine's copy of the circle's secret, empty when it has none yet.
func Circle() ([]byte, error) {
	at, err := circleAt()
	if err != nil {
		return nil, err
	}
	raw, err := keep.ReadFile(at, keep.MaxState)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) != CircleSize {
		return nil, fmt.Errorf("%s is not a circle's secret", at)
	}
	return raw, nil
}

// MakeCircle is the circle's secret, made here when no machine of this user's has made one yet.
func MakeCircle() ([]byte, error) {
	at, err := circleAt()
	if err != nil {
		return nil, err
	}
	var out []byte
	err = keep.While(at, func() error {
		held, err := Circle()
		if err != nil || len(held) > 0 {
			out = held
			return err
		}
		fresh := make([]byte, CircleSize)
		if _, err := io.ReadFull(rand.Reader, fresh); err != nil {
			return err
		}
		out = fresh
		return keep.Replace(at, fresh)
	})
	return out, err
}

// AdoptCircle takes the secret another machine of this user's holds, when this one has none or when
// theirs is the lower of the two: two machines that each made one before they met both end up on the
// same one, whichever of them hears of the other first. It says whether this machine's changed.
func AdoptCircle(theirs []byte) (bool, error) {
	if len(theirs) != CircleSize {
		return false, nil
	}
	at, err := circleAt()
	if err != nil {
		return false, err
	}
	changed := false
	err = keep.While(at, func() error {
		held, err := Circle()
		if err != nil {
			return err
		}
		if len(held) > 0 && bytes.Compare(theirs, held) >= 0 {
			return nil
		}
		changed = true
		return keep.Replace(at, theirs)
	})
	return changed, err
}

// PairSecret is the secret two machines of the circle find each other under: the same for both,
// whichever of them works it out, and different for every pair.
func PairSecret(circle []byte, a, b string) []byte {
	ids := []string{a, b}
	sort.Strings(ids)
	out := make([]byte, 32)
	derive := hkdf.New(sha256.New, circle, []byte(ids[0]+ids[1]), []byte("drop circle pair/1"))
	_, _ = io.ReadFull(derive, out)
	return out
}
