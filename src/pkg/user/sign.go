package user

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// How a badge gets signed, when drop cannot do it itself.
//
// A key in a file drop can read and sign with directly. A key inside hardware it cannot: the
// private half never leaves the device, and talking to it means CTAP, PIV or a vendor's protocol —
// none of which belongs in here, and all of which already have a command that does it.
//
// So the signing is a command. It reads the thing to sign on standard input and writes the
// signature on standard output, which is what `ssh-keygen -Y sign` does and what anything else can
// be made to do. drop supplies a default that works for an SSH key and gets out of the way.

// signer is the command the config named, if it named one.
var signer struct {
	sync.RWMutex
	command string
}

// SignWith names the command that signs badges. Empty leaves drop to work it out.
func SignWith(command string) {
	signer.Lock()
	defer signer.Unlock()

	signer.command = strings.TrimSpace(command)
}

// signCommand is what to run, and empty when drop should sign the key itself.
//
// A key drop can read is signed in process: no command, no touch, nothing to configure. Anything
// else -- a public half whose private key is in hardware or an agent -- needs somebody who can
// reach it, and `ssh-keygen -Y sign` is the one every machine with SSH already has.
func signCommand(where string) string {
	signer.RLock()
	named := signer.command
	signer.RUnlock()

	if named != "" {
		return named
	}
	if !heldElsewhere(where) {
		return ""
	}

	// The private half by OpenSSH's own convention: the same path without .pub. For a key on a
	// security key that is the stub file, and ssh-keygen drives the hardware from it.
	held := strings.TrimSuffix(where, ".pub")
	return fmt.Sprintf("ssh-keygen -Y sign -f %s -n %s", held, Namespace)
}

// heldElsewhere reports whether the file names a key drop cannot sign with itself.
func heldElsewhere(where string) bool {
	raw, err := os.ReadFile(where)
	if err != nil {
		return false
	}
	return !bytes.Contains(raw, []byte("PRIVATE KEY"))
}

// signVia runs the signing command over a message and hands back what it wrote.
//
// Standard error goes through to the terminal rather than being captured: a security key asking for
// a touch says so there, and swallowing it would leave somebody staring at a command that appears
// to have hung.
func signVia(command string, message []byte) ([]byte, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("the signing command is empty")
	}
	for i, at := range parts {
		parts[i] = expand(at)
	}

	run := exec.Command(parts[0], parts[1:]...)
	run.Stdin = bytes.NewReader(message)
	run.Stderr = os.Stderr

	out, err := run.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", parts[0], err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("%s signed nothing", parts[0])
	}
	return out, nil
}

// expand resolves ~ in a command's arguments, because a config is written by a person.
func expand(at string) string {
	if !strings.HasPrefix(at, "~") {
		return at
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return at
	}
	if at == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(at, "~/"))
}
