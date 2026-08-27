package user

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/plain"
)

// A badge is what turns "some machine" into "a machine of bob's".
//
// The transport already proves which machine is calling: iroh authenticates the device key on
// every connection. What it cannot say is whose machine that is. A badge is a statement signed by
// somebody's user key saying that this device is theirs, and it is checked against a user key the
// far end already holds — so nobody is asked anything, and nothing has to be online.
//
// It is signed once, when a machine is enrolled, and presented on every connection after that. A
// signature per connection would mean a touch per connection, which is not a thing anybody would
// use twice.

// Badge is the statement, before it is signed.
type Badge struct {
	// User is who owns the machine.
	User ssh.PublicKey
	// Device is the machine's own identity, as the transport proves it.
	Device string
	// Name is what the machine is called, for a person reading a list.
	Name string
	// Until is when this stops being believed. A machine that is lost stops being one of yours
	// when its badge runs out, which is the only revocation that needs nobody's cooperation.
	Until time.Time
}

// Lasts is how long a new badge is good for.
//
// Long enough that re-signing is rare, which matters when signing costs a touch. Short enough that
// a machine somebody stopped owning does not stay theirs forever.
const Lasts = 90 * 24 * time.Hour

// Bytes is the badge as it is signed and checked: one field per line, in one order, so that what
// was signed and what is read back are the same bytes.
func (b Badge) Bytes() []byte {
	var out strings.Builder

	fmt.Fprintf(&out, "drop-badge/1\n")
	fmt.Fprintf(&out, "user %s", Text(b.User))
	fmt.Fprintf(&out, "device %s\n", b.Device)
	fmt.Fprintf(&out, "name %s\n", b.Name)
	fmt.Fprintf(&out, "until %s\n", b.Until.UTC().Format(time.RFC3339))

	return []byte(out.String())
}

// Expired reports whether a badge has run out.
func (b Badge) Expired(now time.Time) bool { return now.After(b.Until) }

// SignBy makes a badge and has a command sign it, for a key drop cannot sign with itself.
func SignBy(command string, who ssh.PublicKey, device, name string, now time.Time) (Badge, []byte, error) {
	badge := Badge{User: who, Device: device, Name: name, Until: now.Add(Lasts)}
	if err := badge.writable(); err != nil {
		return Badge{}, nil, err
	}

	sig, err := signVia(command, badge.Bytes())
	if err != nil {
		return Badge{}, nil, err
	}
	return badge, sig, nil
}

// writable reports whether a badge can be written down as one line per field and read back as the
// same thing, and whether what it says can be put in front of a person.
//
// The name on a badge is chosen by whoever signs it and printed by whoever reads it. An escape in
// there acts on the terminal it is printed to, so a badge carrying one is refused rather than
// cleaned up: what is checked is the exact bytes that were signed, and a name tidied on the way in
// would mean the string verified and the string shown are not the same string.
func (b Badge) writable() error {
	for what, field := range map[string]string{"device": b.Device, "name": b.Name} {
		if strings.ContainsAny(field, "\r\n") {
			return fmt.Errorf("a badge's %s cannot span lines: %q", what, field)
		}
	}
	if b.Name != "" && !plain.Fit(b.Name, MaxName) {
		return fmt.Errorf("a badge's name is not something that can be shown: %q", b.Name)
	}
	return nil
}

// MaxName bounds the name on a badge: long enough for any machine somebody really has, short
// enough that it cannot fill a row of a listing.
const MaxName = 64

// Sign makes a badge and signs it.
func Sign(by ssh.Signer, device, name string, now time.Time) (Badge, []byte, error) {
	badge := Badge{
		User:   by.PublicKey(),
		Device: device,
		Name:   name,
		Until:  now.Add(Lasts),
	}
	if err := badge.writable(); err != nil {
		return Badge{}, nil, err
	}

	sig, err := Signature(by, badge.Bytes())
	if err != nil {
		return Badge{}, nil, err
	}
	return badge, sig, nil
}

// Read checks a signed badge and reports what it says.
//
// It says nothing about whether the user should be trusted — only that whoever holds that user key
// signed this, for this machine, and that it has not run out. Whether that user is somebody you
// know is the address book's business, and what they may reach is the access rules'.
func Read(signed []byte, sig []byte, now time.Time) (Badge, error) {
	badge, err := parse(signed)
	if err != nil {
		return Badge{}, err
	}

	who, err := Verify(sig, signed, Namespace)
	if err != nil {
		return Badge{}, err
	}

	// The badge names the key that should have signed it, and the signature carries the key that
	// did. A badge signed by somebody else's key is not a badge.
	if string(who.Marshal()) != string(badge.User.Marshal()) {
		return Badge{}, fmt.Errorf("that badge names %s but was signed by %s",
			Fingerprint(badge.User), Fingerprint(who))
	}

	// The same check the signer was held to, applied to what arrived: a badge is only as safe to
	// show as the name inside it.
	if err := badge.writable(); err != nil {
		return Badge{}, err
	}
	if badge.Expired(now) {
		return Badge{}, fmt.Errorf("that badge ran out on %s", badge.Until.UTC().Format(time.RFC3339))
	}
	return badge, nil
}

// parse reads a badge back from what was signed.
//
// One shape, and only one. A line nobody knows, a keyword said twice, or anything else that would
// not be written back exactly as it arrived is not a badge: the signature covers bytes, and a
// reader that quietly forgives a line is a reader that can be shown different bytes to the ones it
// believes it checked.
func parse(signed []byte) (Badge, error) {
	var badge Badge

	lines := strings.Split(strings.TrimRight(string(signed), "\n"), "\n")
	if len(lines) == 0 || lines[0] != "drop-badge/1" {
		return Badge{}, fmt.Errorf("that is not a badge")
	}

	said := map[string]bool{}
	for _, line := range lines[1:] {
		what, rest, found := strings.Cut(line, " ")
		if !found {
			return Badge{}, fmt.Errorf("cannot read %q", line)
		}
		if said[what] {
			return Badge{}, fmt.Errorf("that badge says %q twice", what)
		}
		said[what] = true

		switch what {
		case "user":
			key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(rest))
			if err != nil {
				return Badge{}, fmt.Errorf("the user key in that badge is unreadable: %w", err)
			}
			badge.User = key

		case "device":
			badge.Device = rest

		case "name":
			badge.Name = rest

		case "until":
			at, err := time.Parse(time.RFC3339, rest)
			if err != nil {
				return Badge{}, fmt.Errorf("the date in that badge is unreadable: %w", err)
			}
			badge.Until = at

		default:
			return Badge{}, fmt.Errorf("that badge says %q, which means nothing here", what)
		}
	}

	if badge.User == nil || badge.Device == "" {
		return Badge{}, fmt.Errorf("that badge does not say who owns what")
	}
	if string(badge.Bytes()) != string(signed) {
		return Badge{}, fmt.Errorf("that badge is not written the way a badge is written")
	}
	return badge, nil
}
