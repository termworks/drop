package user

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeKey is a security key in software: an ed25519 key that answers assertions the way a YubiKey
// does, for a credential it was made under.
type fakeKey struct {
	priv    ed25519.PrivateKey
	pub     ssh.PublicKey
	handle  []byte
	counter uint32
	touched bool
}

func newFakeKey(t *testing.T) *fakeKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.ParsePublicKey(ssh.Marshal(skKey{Type: ssh.KeyAlgoSKED25519, Key: pub, Application: "ssh:drop"}))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeKey{priv: priv, pub: key, handle: []byte("the credential"), touched: true}
}

func (k *fakeKey) assert(application string, handle, clientDataHash []byte) ([]byte, error) {
	k.counter++
	app := sha256.Sum256([]byte(application))
	data := append(app[:], 0)
	if k.touched {
		data[len(data)-1] = 0x01
	}
	data = binary.BigEndian.AppendUint32(data, k.counter)
	return append(data, ed25519.Sign(k.priv, append(append([]byte(nil), data...), clientDataHash...))...), nil
}

// asHardware makes this test's user key one held in fake hardware, its handle known here.
func asHardware(t *testing.T) *fakeKey {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	key := newFakeKey(t)
	where, err := Where()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(where), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(where, ssh.MarshalAuthorizedKey(key.pub), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := KeepHandle(Handle{Application: "ssh:drop", Handle: key.handle}); err != nil {
		t.Fatal(err)
	}
	AssertWith(key.assert)
	t.Cleanup(func() { AssertWith(nil) })
	return key
}

// A badge signed through one assertion is the OpenSSH signature every machine already checks.
func TestABadgeIsSignedByOneAssertion(t *testing.T) {
	key := asHardware(t)
	if !CanAssert() {
		t.Fatal("a machine with the key's handle and hardware to reach it cannot sign")
	}

	badge, sig, err := Vouch("some-device", "laptop", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(badge.Bytes(), sig, time.Now())
	if err != nil {
		t.Fatalf("the badge does not check out: %v", err)
	}
	if Text(got.User) != Text(key.pub) || got.Name != "laptop" {
		t.Fatalf("the badge says %s is %q", Text(got.User), got.Name)
	}
}

// An assertion nobody touched for is refused, and so is one for another application.
func TestAnAssertionThatIsNotTheKeysIsRefused(t *testing.T) {
	key := asHardware(t)

	key.touched = false
	if _, _, err := Vouch("some-device", "laptop", time.Now()); err == nil {
		t.Error("an assertion nobody touched for signed a badge")
	}

	key.touched = true
	other := sha256.Sum256([]byte("ssh:other"))
	out, _ := key.assert("ssh:drop", key.handle, make([]byte, 32))
	copy(out, other[:])
	if _, err := assertionSignature("ssh:drop", out); err == nil {
		t.Error("an assertion for another application was taken")
	}
}

// The handle is read out of the file ssh-keygen keeps beside a security key's public half.
func TestTheHandleIsReadFromTheStub(t *testing.T) {
	key := newFakeKey(t)
	var sk skKey
	if err := ssh.Unmarshal(key.pub.Marshal(), &sk); err != nil {
		t.Fatal(err)
	}
	private := ssh.Marshal(struct {
		Check1, Check2 uint32
		Type           string
		Key            []byte
		Application    string
		Flags          uint8
		Handle         []byte
		Reserved       []byte
		Comment        string
	}{7, 7, ssh.KeyAlgoSKED25519, sk.Key, "ssh:drop", 1, []byte("the credential"), nil, "bresilla"})
	body := append([]byte("openssh-key-v1\x00"), ssh.Marshal(struct {
		Cipher, KDF, Options string
		Keys                 uint32
		Public, Private      []byte
	}{"none", "none", "", 1, key.pub.Marshal(), private})...)
	stub := pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: body})

	h, err := readStub(stub)
	if err != nil {
		t.Fatal(err)
	}
	if h.Application != "ssh:drop" || string(h.Handle) != "the credential" {
		t.Fatalf("read %+v", h)
	}
}

// And OpenSSH itself agrees: ssh-keygen checks the signature an assertion made, byte for byte.
func TestOpenSSHChecksASignatureAnAssertionMade(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("needs ssh-keygen")
	}
	key := asHardware(t)
	message := []byte("a badge, as it is signed")
	sig, err := signAssert(message)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	allowed, signature := filepath.Join(dir, "allowed"), filepath.Join(dir, "sig")
	if err := os.WriteFile(allowed, append([]byte("me "), ssh.MarshalAuthorizedKey(key.pub)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signature, sig, 0o600); err != nil {
		t.Fatal(err)
	}
	check := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "me", "-n", Namespace, "-s", signature)
	check.Stdin = bytes.NewReader(message)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen refused it: %v\n%s", err, out)
	}
}
