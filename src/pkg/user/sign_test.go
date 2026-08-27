package user

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A key drop can read is signed here, with no command and nothing to configure.
func TestAKeyDropCanReadNeedsNoCommand(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "key")

	if _, err := make(at); err != nil {
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
