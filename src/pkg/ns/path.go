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

	// The limit is on what comes out, not on what went in. A path given without a leading slash
	// gains one here, so measuring the way in lets through something a character over the limit —
	// and then this refuses its own answer. Two pieces of code that clean the same path a different
	// number of times would disagree about whether it exists at all.
	out := "/" + strings.Join(kept, "/")
	if len(out) > MaxLength {
		return "", fmt.Errorf("path is %d characters, over the %d limit", len(out), MaxLength)
	}
	return out, nil
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

// Address is somewhere to reach: whose machine, which machine, and what on it.
//
//	bob:laptop:/chat   bob's laptop, its /chat
//	laptop:/chat       the machine called laptop
//	bob::/chat         bob, whichever machine of his answers
//	:/chat  /chat      this machine
//	bob:laptop         the machine itself
//	laptop             a machine itself
//
// The three parts are read from the right, so leaving one out leaves out the one on the left. The
// namespace keeps its leading slash, which is what makes it unmistakable for a name and what makes
// what you type the same string the config and `drop path ls` show you.
type Address struct {
	// User is who the machine belongs to, empty when the address did not say.
	User string
	// Machine is what the machine is filed under here, empty when the address named only a user.
	Machine string
	// Path is the namespace, Root when the address named only a machine.
	Path string
	// Here says the address is this machine rather than somebody else's.
	Here bool
}

// ParseAddress reads user:machine:/namespace, and the shorter forms of it.
//
// The names are left as written: a machine may be a local name or a peer id, and only the address
// book knows which. The path is cleaned, because it is the half that becomes a real path.
func ParseAddress(text string) (Address, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Address{}, fmt.Errorf("no address given")
	}

	parts := strings.Split(text, ":")

	// The namespace is the last part, and only when it looks like one. A trailing colon with
	// nothing after it names no namespace, which is how `bob:laptop:` says the machine itself.
	rest := ""
	if last := parts[len(parts)-1]; strings.HasPrefix(last, "/") || last == "" {
		rest, parts = last, parts[:len(parts)-1]
	}

	// What is left are names, and they fill from the right: a machine, then whose it is.
	var user, machine string
	switch len(parts) {
	case 0:
	case 1:
		machine = parts[0]
	case 2:
		user, machine = parts[0], parts[1]
	default:
		return Address{}, fmt.Errorf("%q has more parts than user:machine:/namespace", text)
	}

	for _, name := range []string{user, machine} {
		if strings.Contains(name, "/") {
			return Address{}, fmt.Errorf("%q: a name cannot hold a slash, and a namespace starts with one", text)
		}
	}
	if user == "" && machine == "" && rest == "" {
		return Address{}, fmt.Errorf("%q names nobody", text)
	}

	at := Address{User: user, Machine: machine, Path: Root, Here: user == "" && machine == ""}
	if rest != "" {
		path, err := Clean(rest)
		if err != nil {
			return Address{}, err
		}
		at.Path = path
	}
	return at, nil
}

// Named reports whether the address says which machine, rather than only whose.
func (a Address) Named() bool { return a.Machine != "" }

// String writes an address the shortest way it can still be read back.
func (a Address) String() string {
	if a.Here {
		return a.Path
	}

	who := a.Machine
	if a.User != "" {
		who = a.User + ":" + a.Machine
	}
	if a.Path == Root {
		return who
	}
	return who + ":" + a.Path
}
