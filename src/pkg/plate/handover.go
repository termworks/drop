package plate

import (
	"fmt"
	"strings"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
)

// Moving to another machine.
//
// A name taken from the hardware cannot be carried to new hardware. That is the point of taking it
// from the hardware, and it is also the one thing that has to be possible anyway: machines are
// replaced, and everybody who knew the old one should end up knowing the new one without being
// asked to pair all over again.
//
// So the old machine says it, while it still can, and signs with the key everybody already knows it
// by — its own endpoint key. Nobody has to be told to trust anything new: whoever holds the old
// name can check the statement against it, because the old name is the key that signed it.
//
// It is deliberately one-way and dated. A handover somebody keeps and replays a year later moves a
// machine that has long since moved somewhere else, so it runs out; and it names exactly one
// machine it becomes, so it cannot be pointed at a second one afterwards.

// Moving is how long a handover is good for once it is signed.
//
// Days rather than months. It is carried from one machine to another by somebody who is doing that
// right now, and a statement that redirects a machine is not a thing to leave lying around.
const Moving = 7 * 24 * time.Hour

// Handover is a machine saying what it became.
type Handover struct {
	// Was is the machine everybody knows, and Now is the one they should know instead.
	Was node.ID
	Now node.ID
	// Whose is the account the old machine was reachable as, so a peer updates the right one of
	// several people who were on it.
	Whose string
	// Until is when this stops being acted on.
	Until time.Time
}

// Bytes is the handover as it is signed and read back.
func (h Handover) Bytes() []byte {
	var out strings.Builder

	fmt.Fprintf(&out, "drop-handover/1\n")
	fmt.Fprintf(&out, "was %s\n", h.Was)
	fmt.Fprintf(&out, "now %s\n", h.Now)
	fmt.Fprintf(&out, "whose %s\n", h.Whose)
	fmt.Fprintf(&out, "until %s\n", h.Until.UTC().Format(time.RFC3339))

	return []byte(out.String())
}

// Expired reports whether a handover has run out.
func (h Handover) Expired(now time.Time) bool { return now.After(h.Until) }

// Hand signs this machine over to another one.
//
// Signed with this machine's own endpoint key, which is the key every machine paired with it
// already holds. Nothing new has to be trusted for this to be checkable.
func Hand(to node.ID, whose string, now time.Time) (Handover, []byte, error) {
	sk, err := node.Identity()
	if err != nil {
		return Handover{}, nil, err
	}
	was := sk.Public().EndpointID()

	switch {
	case to.IsZero():
		return Handover{}, nil, fmt.Errorf("a handover has to say which machine this one became")
	case to == was:
		return Handover{}, nil, fmt.Errorf("this machine is already %s", node.Brief(was))
	}
	if err := oneLine(map[string]string{"whose": whose}); err != nil {
		return Handover{}, nil, err
	}

	over := Handover{Was: was, Now: to, Whose: whose, Until: now.Add(Moving)}
	sig := sk.Sign(over.Bytes())
	raw := sig.Bytes()
	return over, raw[:], nil
}

// Took checks a signed handover and reports what it says.
//
// Checked against the machine it says it was, which is the only key that could have signed it. A
// peer acting on this is replacing one name with another in its own address book, so the one thing
// that must hold is that the old machine really said it.
func Took(signed, sig []byte, now time.Time) (Handover, error) {
	over, err := unpick(signed)
	if err != nil {
		return Handover{}, err
	}
	if err := verified(over.Was, signed, sig); err != nil {
		return Handover{}, fmt.Errorf("that handover was not signed by the machine it says it was: %w", err)
	}
	if over.Expired(now) {
		return Handover{}, fmt.Errorf("that handover ran out on %s", over.Until.UTC().Format(time.RFC3339))
	}
	return over, nil
}

// unpick reads a handover back from what was signed, on the same terms a stamp is read on.
func unpick(signed []byte) (Handover, error) {
	var over Handover

	lines := strings.Split(strings.TrimRight(string(signed), "\n"), "\n")
	if len(lines) == 0 || lines[0] != "drop-handover/1" {
		return Handover{}, fmt.Errorf("that is not a handover")
	}

	said := map[string]bool{}
	for _, line := range lines[1:] {
		what, rest, found := strings.Cut(line, " ")
		if !found {
			return Handover{}, fmt.Errorf("cannot read %q", line)
		}
		if said[what] {
			return Handover{}, fmt.Errorf("that handover says %q twice", what)
		}
		said[what] = true

		var err error
		switch what {
		case "was":
			over.Was, err = node.ParseID(rest)
		case "now":
			over.Now, err = node.ParseID(rest)
		case "whose":
			over.Whose = rest
		case "until":
			over.Until, err = time.Parse(time.RFC3339, rest)
		default:
			return Handover{}, fmt.Errorf("that handover says %q, which means nothing here", what)
		}
		if err != nil {
			return Handover{}, fmt.Errorf("the %s in that handover is unreadable: %w", what, err)
		}
	}

	switch {
	case over.Was.IsZero() || over.Now.IsZero() || over.Whose == "":
		return Handover{}, fmt.Errorf("that handover does not say what became what")
	case over.Was == over.Now:
		return Handover{}, fmt.Errorf("that handover says a machine became itself")
	}
	if string(over.Bytes()) != string(signed) {
		return Handover{}, fmt.Errorf("that handover is not written the way a handover is written")
	}
	return over, nil
}
