package user

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func aKey(t *testing.T) ssh.Signer {
	t.Helper()

	_, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	signer, err := ssh.NewSignerFromKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestASignatureCheckksOut(t *testing.T) {
	by := aKey(t)

	sig, err := Signature(by, []byte("this machine is mine"))
	if err != nil {
		t.Fatalf("Signature(): %v", err)
	}

	who, err := Verify(sig, []byte("this machine is mine"), "drop")
	if err != nil {
		t.Fatalf("Verify(): %v", err)
	}
	if string(who.Marshal()) != string(by.PublicKey().Marshal()) {
		t.Error("it reported the wrong key")
	}
}

// The point of a signature is that it stops being one when the message changes.
func TestAChangedMessageDoesNotVerify(t *testing.T) {
	sig, err := Signature(aKey(t), []byte("this machine is mine"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Verify(sig, []byte("this machine is somebody else's"), "drop"); err == nil {
		t.Fatal("a changed message verified")
	}
}

// A signature made for one purpose must not be usable for another. Without this, a badge could be
// replayed as an ssh login, or a git commit signature could become a badge.
func TestASignatureIsBoundToItsNamespace(t *testing.T) {
	sig, err := Signature(aKey(t), []byte("this machine is mine"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Verify(sig, []byte("this machine is mine"), "git"); err == nil {
		t.Fatal("a drop signature verified as a git one")
	}
}

// The format is OpenSSH's, so OpenSSH has to agree. Both directions: what drop writes, ssh-keygen
// must accept; what ssh-keygen writes, drop must accept.
func TestOpenSSHAgreesBothWays(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen to check against")
	}

	dir := t.TempDir()
	key := filepath.Join(dir, "id")

	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "drop test", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("generating a key: %v\n%s", err, out)
	}

	message := []byte("this user owns this machine\n")
	if err := os.WriteFile(filepath.Join(dir, "message"), message, 0o644); err != nil {
		t.Fatal(err)
	}

	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(dir, "allowed")
	if err := os.WriteFile(allowed, []byte("someone "+string(pub)), 0o644); err != nil {
		t.Fatal(err)
	}

	// What drop signs, ssh-keygen verifies.
	raw, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		t.Fatalf("reading the key: %v", err)
	}

	ours, err := Signature(signer, message)
	if err != nil {
		t.Fatalf("Signature(): %v", err)
	}
	mine := filepath.Join(dir, "ours.sig")
	if err := os.WriteFile(mine, ours, 0o644); err != nil {
		t.Fatal(err)
	}

	check := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "someone", "-n", "drop", "-s", mine)
	check.Stdin = strings.NewReader(string(message))

	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen refused a signature drop made: %v\n%s", err, out)
	}

	// What ssh-keygen signs, drop verifies.
	if out, err := exec.Command("ssh-keygen", "-Y", "sign", "-f", key, "-n", "drop", filepath.Join(dir, "message")).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen signing: %v\n%s", err, out)
	}

	theirs, err := os.ReadFile(filepath.Join(dir, "message.sig"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(theirs, message, "drop"); err != nil {
		t.Fatalf("drop refused a signature ssh-keygen made: %v", err)
	}
}
