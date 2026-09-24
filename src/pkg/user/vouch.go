package user

import (
	"bytes"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/wire"
)

// A machine is somebody's when their key signs its badge, and the key does not have to be on it.
//
// A machine that holds the key signs a badge for one that does not — a phone, vouched for by the
// computer the key lives on — and signs it again whenever the two meet and it is running low. Or
// the key itself is carried over, and the machine signs its own from then on. The first keeps the
// key where it was; the second is simpler and makes a lost phone a lost key.

// RenewWithin is how close to running out a vouched badge is when it asks for another. Well inside
// its ninety days, so a phone that meets a computer of yours once a month never runs out, and one
// nobody has seen for three months does.
const RenewWithin = 60 * 24 * time.Hour

// ErrStale is a badge that ran out and could not be signed again here. It is still worn — it
// proves nothing to anybody, and whoever it is shown to judges the machine by their own address
// book — because a node that will not start is worse than one nobody recognises.
var ErrStale = errors.New("this machine's badge ran out and nothing here can sign another")

// Quiet is the user key when it signs here without asking anybody: a private key in a file, with no
// signing command named. A key in hardware or an agent is not quiet, because a badge signed for
// another machine should not cost a touch at a moment nobody chose.
func Quiet() (ssh.Signer, bool) {
	signer.RLock()
	named := signer.command
	signer.RUnlock()
	if named != "" {
		return nil, false
	}

	where, err := Where()
	if err != nil {
		return nil, false
	}
	raw, err := keep.ReadFile(where, keep.MaxState)
	if err != nil {
		return nil, false
	}
	by, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return nil, false
	}
	return by, true
}

// Vouch signs a badge saying another machine is this user's, however this machine signs its own.
func Vouch(device, name string, now time.Time) (Badge, []byte, error) {
	return signFor(device, name, now)
}

// Due reports whether a badge running out then is close enough to it to be signed again.
func Due(until, now time.Time) bool { return until.Sub(now) < RenewWithin }

// Pack is a badge in as few bytes as a camera has to read: everything but the device, which is
// whichever machine reads it — so a code shown for one machine is refused by every other.
func Pack(b Badge, sig []byte) ([]byte, error) {
	block, _ := pem.Decode(sig)
	if block == nil || block.Type != pemType {
		return nil, errors.New("that is not an ssh signature")
	}
	w := wire.NewWriter()
	w.Bytes(b.User.Marshal())
	w.String(b.Name)
	w.Uint(uint64(b.Until.Unix()))
	w.Bytes(block.Bytes)
	return w.Body(), nil
}

// Unpack reads a packed badge as this machine's, and checks it.
func Unpack(raw []byte, now time.Time) (Badge, []byte, error) {
	r := wire.NewReader(raw)
	key, err := r.Bytes(wire.MaxString)
	if err != nil {
		return Badge{}, nil, fmt.Errorf("reading the code: %w", err)
	}
	name, err := r.String(MaxName * 4)
	if err != nil {
		return Badge{}, nil, fmt.Errorf("reading the code: %w", err)
	}
	until, err := r.Uint()
	if err != nil {
		return Badge{}, nil, fmt.Errorf("reading the code: %w", err)
	}
	blob, err := r.Bytes(wire.MaxString)
	if err != nil {
		return Badge{}, nil, fmt.Errorf("reading the code: %w", err)
	}
	if !r.Done() {
		return Badge{}, nil, errors.New("that code says more than a badge does")
	}

	who, err := ssh.ParsePublicKey(key)
	if err != nil {
		return Badge{}, nil, fmt.Errorf("the key in that code is unreadable: %w", err)
	}
	badge := Badge{User: who, Device: deviceID(), Name: name, Until: time.Unix(int64(until), 0).UTC()}
	sig := pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: blob})

	// Checked as the badge it would be for this machine: one signed for any other does not verify.
	if _, err := Read(badge.Bytes(), sig, now); err != nil {
		return Badge{}, nil, fmt.Errorf("that code is not a badge for this machine: %w", err)
	}
	return badge, sig, nil
}

// Wear makes this machine the badge's user's: their key, the public half only, is written where
// this machine's own was, and the badge beside it. The key it had is set aside rather than lost.
func Wear(b Badge, sig []byte, now time.Time) error {
	if _, err := Read(b.Bytes(), sig, now); err != nil {
		return err
	}
	if b.Device != deviceID() {
		return errors.New("that badge is for another machine")
	}
	where, err := ownKey()
	if err != nil {
		return err
	}

	return keep.While(where, func() error {
		if err := setAside(where); err != nil {
			return err
		}
		public := ssh.MarshalAuthorizedKey(b.User)
		if err := keep.Replace(where, public); err != nil {
			return err
		}
		if err := keep.Replace(where+".pub", public); err != nil {
			return err
		}
		return keepBadge(b, sig)
	})
}

// Renewed takes a badge another machine of this user's signed again for this one, and keeps it when
// it is this machine's, this user's, and good for longer than the one it replaces.
func Renewed(bundle []byte, now time.Time) (Badge, []byte, error) {
	signed, sig, err := split(bundle)
	if err != nil {
		return Badge{}, nil, err
	}
	b, err := Read(signed, sig, now)
	if err != nil {
		return Badge{}, nil, err
	}
	if b.Device != deviceID() || !sameUser(b) {
		return Badge{}, nil, errors.New("that badge is not this machine's")
	}

	where, err := badgeAt()
	if err != nil {
		return Badge{}, nil, err
	}
	err = keep.While(where, func() error {
		if held, stored, err := readStored(where); err == nil {
			if was, err := parse(held); err == nil && was.Device == b.Device && !b.Until.After(was.Until) {
				b, sig = was, stored
				return nil
			}
		}
		return keep.Replace(where, append(b.Bytes(), sig...))
	})
	return b, sig, err
}

// Bundle is a badge as it is kept and sent: what was signed, then the signature.
func Bundle(b Badge, sig []byte) []byte { return append(b.Bytes(), sig...) }

// Export is the user key as the thirty-two bytes it is made from, to carry to another machine. Only
// an ed25519 key drop can read: a key in hardware cannot leave it, which is the point of one.
func Export() ([]byte, error) {
	where, err := Where()
	if err != nil {
		return nil, err
	}
	raw, err := keep.ReadFile(where, keep.MaxState)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", where, err)
	}
	key, err := ssh.ParseRawPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not a private key drop can read, so it cannot be carried over", where)
	}
	switch k := key.(type) {
	case ed25519.PrivateKey:
		return k.Seed(), nil
	case *ed25519.PrivateKey:
		return k.Seed(), nil
	}
	return nil, fmt.Errorf("%s is not an ed25519 key, and only one of those can be carried over", where)
}

// Import makes this machine hold a key carried over from another, and signs its badge with it.
func Import(seed []byte) error {
	if len(seed) != ed25519.SeedSize {
		return fmt.Errorf("a key is %d bytes, and that is %d", ed25519.SeedSize, len(seed))
	}
	secret := ed25519.NewKeyFromSeed(seed)
	block, err := ssh.MarshalPrivateKey(secret, "drop user key")
	if err != nil {
		return err
	}
	public, err := ssh.NewPublicKey(secret.Public())
	if err != nil {
		return err
	}
	where, err := ownKey()
	if err != nil {
		return err
	}

	err = keep.While(where, func() error {
		if err := setAside(where); err != nil {
			return err
		}
		if err := keep.Replace(where, pem.EncodeToMemory(block)); err != nil {
			return err
		}
		return keep.Replace(where+".pub", ssh.MarshalAuthorizedKey(public))
	})
	if err != nil {
		return err
	}

	// The badge it wore was signed by the key it had. The next one is signed by this.
	at, err := badgeAt()
	if err != nil {
		return err
	}
	if err := os.Remove(at); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, _, err = Mine(time.Now())
	return err
}

// ownKey is where this machine's own key is, refusing one somebody pointed drop at: replacing the
// key a config names would be replacing somebody's ssh key.
func ownKey() (string, error) {
	if Named() {
		return "", errors.New("this machine's key is one the config or $DROP_USER_KEY names; point that somewhere else instead")
	}
	return Where()
}

// setAside keeps the key a machine had beside the one it is given, so taking another user's key is
// a step that can be walked back by hand. Only the first: a machine that changes hands twice goes
// back to its own key, not to whoever it belonged to in between.
func setAside(where string) error {
	if _, err := os.Stat(where); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, err := os.Stat(where + ".before"); err == nil {
		return nil
	}
	if err := os.Rename(where, where+".before"); err != nil {
		return fmt.Errorf("setting %s aside: %w", where, err)
	}
	_ = os.Rename(where+".pub", where+".before.pub")
	return nil
}

func keepBadge(b Badge, sig []byte) error {
	at, err := badgeAt()
	if err != nil {
		return err
	}
	return keep.Replace(at, Bundle(b, sig))
}

// split is a kept badge taken apart into what was signed and the signature over it.
func split(bundle []byte) ([]byte, []byte, error) {
	at := bytes.Index(bundle, signatureMarker)
	if at < 0 {
		return nil, nil, errors.New("that is not a signed badge")
	}
	return bundle[:at], bundle[at:], nil
}

// signFor makes a badge for a machine, whichever way the key can be reached.
//
// A key drop can read is signed here and now. A key it cannot -- one in hardware, or held by an
// agent -- is signed by the command that can reach it, which is the config's to name and
// `ssh-keygen -Y sign` by default.
func signFor(device, name string, now time.Time) (Badge, []byte, error) {
	where, err := Where()
	if err != nil {
		return Badge{}, nil, err
	}

	// A security key this machine reaches itself, which on a phone is the only way it signs.
	if CanAssert() {
		return signedWithHardware(device, name, now)
	}

	if command := signCommand(where); command != "" {
		who, err := Public()
		if err != nil {
			return Badge{}, nil, err
		}
		return SignBy(command, who, device, name, now)
	}

	by, err := Signer()
	if err != nil {
		return Badge{}, nil, err
	}
	return Sign(by, device, name, now)
}

// Leave takes this machine back out of whoever's it became: the key it had before is put back, or
// when there was none, the next start makes one. Its badge goes with the user it named.
func Leave() error {
	where, err := ownKey()
	if err != nil {
		return err
	}
	forgetHandle()
	err = keep.While(where, func() error {
		for _, file := range []string{where, where + ".pub"} {
			if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if _, err := os.Stat(where + ".before"); err == nil {
			if err := os.Rename(where+".before", where); err != nil {
				return err
			}
			_ = os.Rename(where+".before.pub", where+".pub")
		}
		return nil
	})
	if err != nil {
		return err
	}
	at, err := badgeAt()
	if err != nil {
		return err
	}
	if err := os.Remove(at); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, _, err = Mine(time.Now())
	return err
}

// Took reports whether this machine set a key of its own aside to become somebody's, which is what
// leaving puts back.
func Took() bool {
	where, err := Where()
	if err != nil || Named() {
		return false
	}
	_, err = os.Stat(where + ".before")
	return err == nil
}
