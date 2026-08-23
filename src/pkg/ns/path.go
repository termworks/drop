// Package ns is drop's namespace layer: one node id, many typed paths under it.
package ns

import (
	"fmt"
	"strings"
)

// MaxDepth and MaxLength bound a path, so a peer cannot make the lookup table walk forever.
const (
	MaxDepth  = 16
	MaxLength = 512
)

// Root is the path a bare address means.
const Root = "/"

// Clean turns whatever was written into the one spelling drop stores and compares.
//
//	stream/1        →  /stream/1
//	/stream/1/      →  /stream/1
//	//stream//1     →  /stream/1
func Clean(path string) (string, error) {
	if len(path) > MaxLength {
		return "", fmt.Errorf("path is %d characters, over the %d limit", len(path), MaxLength)
	}

	parts := strings.Split(path, "/")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if err := checkSegment(part); err != nil {
			return "", err
		}
		kept = append(kept, part)
	}

	if len(kept) > MaxDepth {
		return "", fmt.Errorf("path is %d segments deep, over the %d limit", len(kept), MaxDepth)
	}
	if len(kept) == 0 {
		return Root, nil
	}
	return "/" + strings.Join(kept, "/"), nil
}

// checkSegment keeps a path to what can be typed, logged and compared without surprises.
func checkSegment(part string) error {
	if part == "." || part == ".." {
		return fmt.Errorf("%q is not allowed in a path", part)
	}
	for _, r := range part {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return fmt.Errorf("%q is not allowed in a path: use lowercase letters, digits, - _ .", string(r))
		}
	}
	return nil
}

// Address is a peer and a path under it: `laptop/stream/1`.
type Address struct {
	Peer string
	Path string
}

// ParseAddress splits `peer/path`. A bare peer addresses its root.
//
// The peer half is left as written, because it may be a local name or a peer id, and only the
// address book knows which.
func ParseAddress(text string) (Address, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Address{}, fmt.Errorf("no address given")
	}

	peer, rest, found := strings.Cut(text, "/")
	if peer == "" {
		return Address{}, fmt.Errorf("%q has no peer before the path", text)
	}
	if !found {
		return Address{Peer: peer, Path: Root}, nil
	}

	path, err := Clean(rest)
	if err != nil {
		return Address{}, err
	}
	return Address{Peer: peer, Path: path}, nil
}

func (a Address) String() string {
	if a.Path == Root {
		return a.Peer
	}
	return a.Peer + a.Path
}
