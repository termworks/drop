package user

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
)

// A user key in a security key that this machine reaches itself: a phone holding a YubiKey to its
// back, or one plugged into it.
//
// A computer signs with a YubiKey through ssh-keygen, which drives it. A phone has no ssh-keygen,
// but it can talk to the YubiKey directly, and all a YubiKey is asked for is a FIDO assertion: a
// signature over the application's hash, a counter and a hash the caller chooses. That is exactly
// what an sk-ssh-ed25519 signature is. So the phone asks the key for one assertion and this builds
// the rest — the same OpenSSH signature ssh-keygen would have written, which every machine already
// checks.
//
// The key is named by its handle, which ssh-keygen keeps in the file beside the public half. It is
// not a secret — it does nothing without the YubiKey — so the machine that has it hands it to the
// rest of this user's machines, and a phone that is one of them signs with a tap and nothing else.

// Asserter has a security key make one assertion: over clientDataHash, for the credential handle,
// under application. It hands back the authenticator data and the signature, one after the other.
type Asserter func(application string, handle, clientDataHash []byte) ([]byte, error)

var hardware struct {
	sync.RWMutex
	assert Asserter
}

// AssertWith lets this machine's own security key sign for the user key.
func AssertWith(a Asserter) {
	hardware.Lock()
	defer hardware.Unlock()
	hardware.assert = a
}

// Handle names a user key held in a security key: the application it was made under, and the
// credential that is it.
type Handle struct {
	Application string `json:"application"`
	Handle      []byte `json:"handle"`
}

// skKey is an sk-ssh-ed25519 public key's fields, as the wire has them.
type skKey struct {
	Type        string
	Key         []byte
	Application string
}

// skOf is a key's fields when it is one a security key holds.
func skOf(key ssh.PublicKey) (skKey, bool) {
	if key == nil || key.Type() != ssh.KeyAlgoSKED25519 {
		return skKey{}, false
	}
	var out skKey
	if err := ssh.Unmarshal(key.Marshal(), &out); err != nil {
		return skKey{}, false
	}
	return out, true
}

func handleAt() (string, error) {
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "handle.json"), nil
}

// KnownHandle is the handle this machine holds for the user key, from the stub beside it or handed
// over by another machine of this user's.
func KnownHandle() (Handle, bool) {
	if h, err := stubHandle(); err == nil {
		return h, true
	}
	at, err := handleAt()
	if err != nil {
		return Handle{}, false
	}
	raw, err := keep.ReadFile(at, keep.MaxState)
	if err != nil {
		return Handle{}, false
	}
	var h Handle
	if json.Unmarshal(raw, &h) != nil || len(h.Handle) == 0 {
		return Handle{}, false
	}
	return h, true
}

// KeepHandle writes down a handle another machine of this user's handed over, when it is for the
// key this machine is this user's under.
func KeepHandle(h Handle) error {
	pub, err := Public()
	if err != nil {
		return err
	}
	sk, ok := skOf(pub)
	if !ok || sk.Application != h.Application || len(h.Handle) == 0 {
		return nil
	}
	if held, ok := KnownHandle(); ok && bytes.Equal(held.Handle, h.Handle) {
		return nil
	}
	at, err := handleAt()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return keep.Replace(at, raw)
}

// CanAssert says this machine signs for the user key with its own security key: the key is one,
// its handle is known here, and something here can reach the hardware.
func CanAssert() bool {
	hardware.RLock()
	a := hardware.assert
	hardware.RUnlock()
	if a == nil {
		return false
	}
	pub, err := Public()
	if err != nil {
		return false
	}
	if _, ok := skOf(pub); !ok {
		return false
	}
	_, ok := KnownHandle()
	return ok
}

// signAssert is an OpenSSH signature over message by the user key, made by one assertion.
func signAssert(message []byte) ([]byte, error) {
	hardware.RLock()
	a := hardware.assert
	hardware.RUnlock()
	pub, err := Public()
	if err != nil {
		return nil, err
	}
	sk, ok := skOf(pub)
	if !ok || a == nil {
		return nil, errors.New("the user key is not one a security key here can sign with")
	}
	h, ok := KnownHandle()
	if !ok || h.Application != sk.Application {
		return nil, errors.New("this machine does not know which credential on the security key is the user key")
	}

	sum := sha256.Sum256(sshsigBlob(message))
	out, err := a(sk.Application, h.Handle, sum[:])
	if err != nil {
		return nil, err
	}
	sig, err := assertionSignature(sk.Application, out)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: armour(pub, Namespace, sig)}), nil
}

// assertionSignature turns what a security key handed back into the signature ssh reads: the
// authenticator data is the application's hash, a flags byte and a four-byte counter, and what the
// key signed is that followed by the hash it was given.
func assertionSignature(application string, out []byte) (*ssh.Signature, error) {
	const data = sha256.Size + 1 + 4
	if len(out) <= data {
		return nil, fmt.Errorf("the security key handed back %d bytes, which is not an assertion", len(out))
	}
	app := sha256.Sum256([]byte(application))
	if !bytes.Equal(out[:sha256.Size], app[:]) {
		return nil, errors.New("the security key signed for another application")
	}
	if out[sha256.Size]&0x01 == 0 {
		return nil, errors.New("the security key signed without anybody touching it")
	}
	return &ssh.Signature{
		Format: ssh.KeyAlgoSKED25519,
		Blob:   append([]byte(nil), out[data:]...),
		Rest:   append([]byte(nil), out[sha256.Size:data]...),
	}, nil
}

// sshsigBlob is what an OpenSSH signature over message puts through the key.
func sshsigBlob(message []byte) []byte {
	return sshsigBlobIn(Namespace, message)
}

// stubHandle reads the handle from the file ssh-keygen keeps beside a security key's public half.
func stubHandle() (Handle, error) {
	where, err := Where()
	if err != nil {
		return Handle{}, err
	}
	raw, err := keep.ReadFile(strings.TrimSuffix(where, ".pub"), keep.MaxState)
	if err != nil {
		return Handle{}, err
	}
	return readStub(raw)
}

// readStub reads a security key's handle out of an OpenSSH private key file, which for such a key
// holds no secret: the application, some flags, and the handle the hardware knows the key by.
func readStub(raw []byte) (Handle, error) {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
		return Handle{}, errors.New("that is not an OpenSSH key file")
	}
	const auth = "openssh-key-v1\x00"
	if !bytes.HasPrefix(block.Bytes, []byte(auth)) {
		return Handle{}, errors.New("that key file has the wrong shape")
	}
	var file struct {
		Cipher  string
		KDF     string
		Options string
		Keys    uint32
		Public  []byte
		Private []byte
	}
	if err := ssh.Unmarshal(block.Bytes[len(auth):], &file); err != nil {
		return Handle{}, fmt.Errorf("reading the key file: %w", err)
	}
	if file.Cipher != "none" {
		return Handle{}, errors.New("the key file is locked with a passphrase")
	}
	var key struct {
		Check1, Check2 uint32
		Type           string
		Key            []byte
		Application    string
		Flags          uint8
		Handle         []byte
		Reserved       []byte
		Rest           []byte `ssh:"rest"`
	}
	if err := ssh.Unmarshal(file.Private, &key); err != nil {
		return Handle{}, fmt.Errorf("reading the key in the file: %w", err)
	}
	if key.Check1 != key.Check2 || key.Type != ssh.KeyAlgoSKED25519 || len(key.Handle) == 0 {
		return Handle{}, errors.New("that key file does not hold an ed25519 security key")
	}
	return Handle{Application: key.Application, Handle: key.Handle}, nil
}

// signedWithHardware is a badge signed by one assertion.
func signedWithHardware(device, name string, now time.Time) (Badge, []byte, error) {
	pub, err := Public()
	if err != nil {
		return Badge{}, nil, err
	}
	badge := Badge{User: pub, Device: device, Name: name, Until: now.Add(Lasts)}
	if err := badge.writable(); err != nil {
		return Badge{}, nil, err
	}
	sig, err := signAssert(badge.Bytes())
	if err != nil {
		return Badge{}, nil, err
	}
	return badge, sig, nil
}

// forgetHandle drops a handle handed over, which is what leaving takes with it.
func forgetHandle() {
	if at, err := handleAt(); err == nil {
		_ = os.Remove(at)
	}
}
