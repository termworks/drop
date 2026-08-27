package proto

import (
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/plate"
	"github.com/bresilla/drop/src/pkg/user"
	"github.com/bresilla/drop/src/pkg/wire"
)

// What this machine shows to say whose it is.
//
// The protocol does not know how a badge is made or checked — that is the user package's business,
// and this one must not depend on it or the two would import each other. So the badge is handed in
// once at startup and attached to whatever this node opens.

var mine struct {
	sync.RWMutex
	badge  []byte
	signed []byte
}

// Carry sets the badge every session this node opens will present. Called once, at startup.
func Carry(badge, signed []byte) {
	mine.Lock()
	defer mine.Unlock()

	mine.badge, mine.signed = badge, signed
}

// carried is what to attach. Every node has one: identity is not optional, and a node that could
// not make a badge said so and stopped before it got this far.
func carried() ([]byte, []byte) {
	mine.RLock()
	defer mine.RUnlock()

	return mine.badge, mine.signed
}

// Badged is what a caller's badge turned out to say, once it checked out.
//
// It is only ever the result of a signature that verified against the key inside it, and of a
// device that matches the one the transport authenticated. It says whose machine is calling. It
// says nothing about whether that person is welcome — the address book decides that, and the
// namespace rules decide what they may reach.
type Badged struct {
	// Key is the user key, written the way authorized_keys writes one. Empty when the badge did
	// not check out, which is a caller nothing is known about rather than one to trust less.
	Key string
	// As is what that person calls this machine. Their label, not ours.
	As string
}

// Shown reports whether there is anything here to look up.
func (b Badged) Shown() bool { return b.Key != "" }

// vouched checks a caller's badge against the machine the transport proved it is.
//
// A badge that verifies for a different device is somebody replaying somebody else's, so the
// device has to match. What does not check out yields nothing rather than an error: refusing the
// connection outright would turn a clock skew or an expired badge into a device that has vanished,
// when the right answer is a caller whose person is not established and whose paths say so.
func vouched(from node.ID, open Opening) Badged {
	if len(open.Badge) == 0 || len(open.Signed) == 0 {
		return Badged{}
	}

	badge, err := user.Read(open.Badge, open.Signed, time.Now())
	if err != nil || badge.Device != from.String() {
		return Badged{}
	}
	return Badged{Key: user.Text(badge.User), As: badge.Name}
}

// showable is a badge on the wire, for the frames that carry nothing else.
//
// Hello is one of those: the ask is otherwise an empty frame, and what a node serves depends on who
// is asking, so the badge rides in that frame's body.
func showable() []byte {
	badge, signed := carried()

	w := wire.NewWriter()
	w.Bytes(badge)
	w.Bytes(signed)
	return w.Body()
}

// showing reads a badge out of such a frame and checks it.
func showing(from node.ID, body []byte) Badged {
	r := wire.NewReader(body)
	badge, err := r.Bytes(wire.MaxString)
	if err != nil {
		return Badged{}
	}
	signed, err := r.Bytes(wire.MaxString)
	if err != nil {
		return Badged{}
	}
	return vouched(from, Opening{Badge: badge, Signed: signed})
}

// What this machine shows to say what it is running on.
//
// Kept the same way the badge is and for the same reason: this package must not import the one that
// makes the thing, or the two would import each other.

var plated struct {
	sync.RWMutex
	plate   []byte
	stamped []byte
}

// Stamped sets the plate every session this node opens will present. Called once, at startup, and
// not at all on a machine that will not say what it is — which is allowed, and costs only that
// nobody learns which machine this drop is on.
func Stamped(plate, stamped []byte) {
	plated.Lock()
	defer plated.Unlock()

	plated.plate, plated.stamped = plate, stamped
}

// stamping is what to attach, and nothing at all when this machine had nothing to say.
func stamping() ([]byte, []byte) {
	plated.RLock()
	defer plated.RUnlock()

	return plated.plate, plated.stamped
}

// Stood is what a caller's plate turned out to say, once it checked out.
//
// It says which machine the caller's drop is running on and which account there it belongs to. It
// says nothing about who that is: everyone with an account on a machine can produce that machine's
// stamp, by design, so this establishes hardware and never personhood. The badge is what says who.
type Stood struct {
	// Machine is the machine, written the way an id is written. Empty when nothing checked out.
	Machine string
	// Whose is the account on it, as that machine tells its own accounts apart.
	Whose string
}

// Shown reports whether there is anything here.
func (s Stood) Shown() bool { return s.Machine != "" }

// stood checks a caller's plate against the machine the transport proved it is.
//
// The stamp names the endpoint it is for, and the transport has already proved which endpoint is
// calling. A stamp for a different one is somebody replaying a stamp that was not about them, so
// the two have to agree. What does not check out yields nothing rather than an error, for the same
// reason a badge does: an expired stamp is a caller less is known about, not a caller to hang up on.
func stood(from node.ID, open Opening) Stood {
	if len(open.Plate) == 0 || len(open.Stamped) == 0 {
		return Stood{}
	}

	stamp, err := plate.Read(open.Plate, open.Stamped, time.Now())
	if err != nil || stamp.Endpoint != from {
		return Stood{}
	}
	return Stood{Machine: stamp.Machine.String(), Whose: stamp.Whose}
}
