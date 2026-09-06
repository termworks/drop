package user

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A key drop can read is signed here, with no command and nothing to configure.
func TestAKeyDropCanReadNeedsNoCommand(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "key")

	if _, err := makeKey(at); err != nil {
		t.Fatal(err)
	}
	if got := signCommand(at); got != "" {
		t.Errorf("a readable key wanted a command: %q", got)
	}
}

// A key drop cannot read is signed by whoever can reach it, and ssh-keygen is the default because
// every machine with SSH already has it.
func TestAKeyHeldElsewhereGetsTheDefaultCommand(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "id_yubi.pub")

	pub := "sk-ssh-ed25519@openssh.com AAAAGnNr bresilla@core\n"
	if err := os.WriteFile(at, []byte(pub), 0o644); err != nil {
		t.Fatal(err)
	}

	got := signCommand(at)
	if !strings.Contains(got, "ssh-keygen -Y sign") {
		t.Fatalf("command = %q", got)
	}
	if !strings.Contains(got, "-n drop") {
		t.Errorf("the drop namespace is missing: %q", got)
	}
	// The private half by OpenSSH's convention, which for a security key is the stub file.
	if !strings.Contains(got, filepath.Join(dir, "id_yubi")) || strings.Contains(got, ".pub -n") {
		t.Errorf("it did not point at the private half: %q", got)
	}
}

// Whatever the config names wins, so anything that can talk to the hardware can be used.
func TestAConfiguredCommandWins(t *testing.T) {
	SignWith("ykman-or-whatever --sign")
	defer SignWith("")

	if got := signCommand("/anything/at/all.pub"); got != "ykman-or-whatever --sign" {
		t.Errorf("command = %q", got)
	}
}

// The command reads the message on stdin and writes the signature on stdout, and nothing at all
// coming back is a failure rather than an empty signature nobody can check.
func TestASigningCommandThatSaysNothingFails(t *testing.T) {
	if _, err := signVia("true", []byte("sign me")); err == nil {
		t.Error("a command that signed nothing was believed")
	}
	out, err := signVia("cat", []byte("sign me"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "sign me" {
		t.Errorf("the message did not reach the command: %q", out)
	}
}

func TestASigningCommandCannotRunForever(t *testing.T) {
	started := time.Now()
	_, err := signViaWithin("sleep 10", []byte("sign me"), 50*time.Millisecond, maxCommandSignature)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a stuck signer returned %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a stuck signer took %s to stop", elapsed)
	}
}

func TestASigningCommandCannotFillMemory(t *testing.T) {
	_, err := signViaWithin("printf "+strings.Repeat("x", 128), nil, time.Second, 32)
	if err == nil || !strings.Contains(err.Error(), "more than 32") {
		t.Fatalf("an oversized signature returned %v", err)
	}
}
