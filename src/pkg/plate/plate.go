// Package plate is what a machine says about itself: which endpoints are on it, and what it became.
//
// Two statements, both signed, both checkable by somebody who was not there.
//
// A stamp says "this endpoint is one of the drops running on me, and it belongs to that account".
// It is what lets several people with accounts on one machine be seen as several people on one
// machine, rather than as several unrelated machines that happen to answer at the same time.
//
// A handover says "I was this machine, and I am now that one". A name taken from the hardware
// cannot be carried to new hardware, which is the point of taking it from the hardware — so
// replacing a machine has to be something the old one says out loud, once, while it still can.
package plate

import (
	"fmt"
	"strings"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/metal"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/plain"
)

// Machine is the key standing for this machine, which is not the key standing for this drop.
//
// Every account here derives the same one, and that is deliberate: it is what makes them one
// machine. It also means a stamp proves hardware and not personhood — see the note on metal.Machine.
// A machine that will not say what it is has none, and says so rather than inventing one.
func Machine() (key.SecretKey, error) {
	mark := metal.Read()
	if !mark.Held() {
		return key.SecretKey{}, fmt.Errorf("this machine says nothing about itself, so it cannot vouch for anything")
	}

	seed, err := mark.Machine()
	if err != nil {
		return key.SecretKey{}, err
	}
	return key.NewSecretKey(seed), nil
}

// MachineID is what this machine is called when it speaks for itself.
func MachineID() (node.ID, error) {
	sk, err := Machine()
	if err != nil {
		return node.ID{}, err
	}
	return sk.Public().EndpointID(), nil
}

// Lasts is how long a stamp is good for. Long enough not to be a chore, short enough that an
// account closed on a machine stops being on it without anybody having to go and revoke it.
const Lasts = 90 * 24 * time.Hour

// Stamp is a machine saying an endpoint is one of its own.
type Stamp struct {
	// Machine is the machine, and Endpoint is the drop running on it.
	Machine  node.ID
	Endpoint node.ID
	// Whose is the account it belongs to, which is what tells two people on one machine apart.
	Whose string
	// Until is when this stops being believed.
	Until time.Time
}

// Bytes is the stamp as it is signed and read back: one field to a line, in one order, so that what
// was signed and what is checked are the same bytes.
func (s Stamp) Bytes() []byte {
	var out strings.Builder

	fmt.Fprintf(&out, "drop-plate/1\n")
	fmt.Fprintf(&out, "machine %s\n", s.Machine)
	fmt.Fprintf(&out, "endpoint %s\n", s.Endpoint)
	fmt.Fprintf(&out, "whose %s\n", s.Whose)
	fmt.Fprintf(&out, "until %s\n", s.Until.UTC().Format(time.RFC3339))

	return []byte(out.String())
}

// Expired reports whether a stamp has run out.
func (s Stamp) Expired(now time.Time) bool { return now.After(s.Until) }

// Sign stamps the drop running here as one of this machine's own.
func Sign(now time.Time) (Stamp, []byte, error) {
	sk, err := Machine()
	if err != nil {
		return Stamp{}, nil, err
	}
	here, err := node.LocalID()
	if err != nil {
		return Stamp{}, nil, err
	}

	stamp := Stamp{
		Machine:  sk.Public().EndpointID(),
		Endpoint: here,
		Whose:    metal.Whose(),
		Until:    now.Add(Lasts),
	}
	if err := oneLine(map[string]string{"whose": stamp.Whose}); err != nil {
		return Stamp{}, nil, err
	}

	sig := sk.Sign(stamp.Bytes())
	raw := sig.Bytes()
	return stamp, raw[:], nil
}

// Read checks a signed stamp and reports what it says.
//
// What it establishes is narrow and worth being exact about: that whoever holds the machine key for
// the machine named here signed this, for this endpoint, and that it has not run out. Everyone with
// an account on that machine holds that key, so this says which hardware an endpoint is on and says
// nothing whatever about who is behind it.
func Read(signed, sig []byte, now time.Time) (Stamp, error) {
	stamp, err := parse(signed)
	if err != nil {
		return Stamp{}, err
	}
	if err := verified(stamp.Machine, signed, sig); err != nil {
		return Stamp{}, fmt.Errorf("that stamp does not check out against the machine it names: %w", err)
	}
	if stamp.Expired(now) {
		return Stamp{}, fmt.Errorf("that stamp ran out on %s", stamp.Until.UTC().Format(time.RFC3339))
	}
	return stamp, nil
}

// verified checks a signature against the key an id is.
func verified(id node.ID, message, sig []byte) error {
	if len(sig) != key.SignatureSize {
		return fmt.Errorf("a signature is %d bytes and that one is %d", key.SignatureSize, len(sig))
	}
	var raw [key.SignatureSize]byte
	copy(raw[:], sig)

	return id.PublicKey().Verify(message, key.NewSignature(raw))
}

// oneLine refuses a field that would be read back as something other than what was written, or
// that could act on the terminal it is printed to.
//
// Refused rather than cleaned up. A stamp is checked against the exact bytes somebody signed: a
// name tidied on the way in would mean the string that was verified and the string that is shown
// are two different strings, and only one of them was signed. So a name that is not fit to print
// makes the whole stamp not a stamp.
func oneLine(fields map[string]string) error {
	for what, field := range fields {
		if field == "" {
			return fmt.Errorf("a stamp needs a %s", what)
		}
		if !plain.Fit(field, MaxName) {
			return fmt.Errorf("a stamp's %s is not something that can be shown: %q", what, field)
		}
	}
	return nil
}

// MaxName bounds a name inside something signed. Long enough for any account somebody really has,
// short enough that it cannot fill a row.
const MaxName = 64

// parse reads a stamp back from what was signed.
//
// One shape, and only one. A line nobody knows, a keyword said twice, or anything that would not be
// written back exactly as it arrived is not a stamp: the signature covers bytes, and a reader that
// forgives a line is a reader that can be shown different bytes to the ones it checked.
func parse(signed []byte) (Stamp, error) {
	var stamp Stamp

	lines := strings.Split(strings.TrimRight(string(signed), "\n"), "\n")
	if len(lines) == 0 || lines[0] != "drop-plate/1" {
		return Stamp{}, fmt.Errorf("that is not a stamp")
	}

	said := map[string]bool{}
	for _, line := range lines[1:] {
		what, rest, found := strings.Cut(line, " ")
		if !found {
			return Stamp{}, fmt.Errorf("cannot read %q", line)
		}
		if said[what] {
			return Stamp{}, fmt.Errorf("that stamp says %q twice", what)
		}
		said[what] = true

		var err error
		switch what {
		case "machine":
			stamp.Machine, err = node.ParseID(rest)
		case "endpoint":
			stamp.Endpoint, err = node.ParseID(rest)
		case "whose":
			stamp.Whose = rest
		case "until":
			stamp.Until, err = time.Parse(time.RFC3339, rest)
		default:
			return Stamp{}, fmt.Errorf("that stamp says %q, which means nothing here", what)
		}
		if err != nil {
			return Stamp{}, fmt.Errorf("the %s in that stamp is unreadable: %w", what, err)
		}
	}

	if stamp.Machine.IsZero() || stamp.Endpoint.IsZero() {
		return Stamp{}, fmt.Errorf("that stamp does not say what is on what")
	}
	// Checked on the way in as well as on the way out: what is signed here is signed by a machine,
	// and a machine that would sign a name full of escapes is exactly the one to refuse.
	if err := oneLine(map[string]string{"whose": stamp.Whose}); err != nil {
		return Stamp{}, err
	}
	if string(stamp.Bytes()) != string(signed) {
		return Stamp{}, fmt.Errorf("that stamp is not written the way a stamp is written")
	}
	return stamp, nil
}
