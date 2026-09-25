package mobile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/user"
)

// Who this phone's person is: an SSH key handed to the phone, or the key in a YubiKey held to it.
// Either is kept in the phone's own storage and named in the config it starts from, so it is still
// who the phone is after the app writes that config again.

// UseKeyFile makes an SSH private key who this phone's person is, and says what it is now. The
// phone's machines that hold the same key find it by themselves.
func (n *Node) UseKeyFile(name string, raw []byte) (string, error) {
	signer, err := ssh.ParsePrivateKey(raw)
	if errors.As(err, new(*ssh.PassphraseMissingError)) {
		return "", errors.New("that key is locked with a passphrase, and a phone signs when nobody is there to type it")
	}
	if err != nil {
		return "", errors.New("that is not an SSH private key")
	}
	at, err := keyFile(name)
	if err != nil {
		return "", err
	}
	if err := keep.Replace(at, raw); err != nil {
		return "", err
	}
	_ = os.Chmod(at, 0o600)
	_ = keep.Replace(at+".pub", ssh.MarshalAuthorizedKey(signer.PublicKey()))
	return n.chose(at)
}

// UseYubiKey makes the key in a YubiKey who this phone's person is, from what the phone read off
// it: the credential's application, its ed25519 key and its handle. Signing this phone's badge
// asks for the YubiKey once more.
func (n *Node) UseYubiKey(application string, key, handle []byte) (string, error) {
	pub, err := user.HardwareKey(application, key)
	if err != nil {
		return "", err
	}
	at, err := keyFile("yubikey.pub")
	if err != nil {
		return "", err
	}
	if err := keep.Replace(at, ssh.MarshalAuthorizedKey(pub)); err != nil {
		return "", err
	}
	if err := user.KeepHandleFor(pub, user.Handle{Application: application, Handle: handle}); err != nil {
		return "", err
	}
	return n.chose(at)
}

// chose makes the key at a file this phone's, and remembers it for the config written next.
func (n *Node) chose(at string) (string, error) {
	now, err := n.back.UseKey(at)
	if err != nil {
		return "", err
	}
	dir, err := node.ConfigDir()
	if err != nil {
		return now, err
	}
	changed(n.events)
	return now, keep.Replace(filepath.Join(dir, chosenKey), []byte(at))
}

// keyFile is where a key this phone was given is kept.
func keyFile(name string) (string, error) {
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || strings.HasPrefix(name, ".") {
		name = "id_ed25519"
	}
	keys := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(keys, name), nil
}

// chosenKey is the file beside the config that names the key this phone's person chose.
const chosenKey = "chosen-key"

// chosenLine is the config line naming the chosen key, when one was chosen and is still there.
func chosenLine(configDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(configDir, "drop", chosenKey))
	if err != nil {
		return "", nil
	}
	at := strings.TrimSpace(string(raw))
	if _, err := os.Stat(at); err != nil || strings.Contains(at, "]==]") {
		return "", nil
	}
	return fmt.Sprintf("drop.user_key = [==[%s]==]\n", at), nil
}
