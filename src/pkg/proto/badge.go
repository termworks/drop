package proto

import (
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
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

// carried is what to attach, and nothing at all when this node has no badge.
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
	// Key is the user key, written the way authorized_keys writes one. Empty when the caller
	// showed no badge, or showed one that did not check out.
	Key string
	// As is what that person calls this machine. Their label, not ours.
	As string
}

// Shown reports whether there is anything here to look up.
func (b Badged) Shown() bool { return b.Key != "" }

// vouched checks a caller's badge against the machine the transport proved it is.
//
// A badge that verifies for a different device is somebody replaying somebody else's, so the
// device has to match. Anything that does not check out is not an error to report: the caller is
// simply a machine with no person behind it, which is what every node was before badges existed.
func vouched(from node.ID, open Open) Badged {
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
// Hello is one of those: it is asked with an empty frame, and an empty frame is what a node too old
// to know about badges sends. Putting the badge in that frame's body means the ask still looks the
// same to them, and the answer still comes back.
func showable() []byte {
	badge, signed := carried()
	if len(badge) == 0 {
		return nil
	}

	w := wire.NewWriter()
	w.Bytes(badge)
	w.Bytes(signed)
	return w.Body()
}

// showing reads a badge out of such a frame and checks it. An empty body is a node with nothing to
// show, which is not a failure.
func showing(from node.ID, body []byte) Badged {
	if len(body) == 0 {
		return Badged{}
	}

	r := wire.NewReader(body)
	badge, err := r.Bytes(wire.MaxString)
	if err != nil {
		return Badged{}
	}
	signed, err := r.Bytes(wire.MaxString)
	if err != nil {
		return Badged{}
	}
	return vouched(from, Open{Badge: badge, Signed: signed})
}
