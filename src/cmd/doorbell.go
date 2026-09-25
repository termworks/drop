package cmd

import (
	"context"
	"log"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/rendezvous"
	"github.com/bresilla/drop/src/pkg/user"
)

// Joining your machines by holding your key.
//
// A machine that is given your key — your SSH key, or your YubiKey — is yours: it signs its own
// badge. What it lacks is the rest of your machines, which it has never met. So it rings for them
// (see the doorbell in rendezvous), and every machine of yours that chose the same key answers: it
// checks the badge the ring carries, writes the new machine down as yours, and the hello and the
// shared address book do the rest. No code, and nothing to say yes to, because the key already said
// it.
//
// Only a key somebody chose rings. A key drop made for itself is on this machine alone, and there
// is nobody else to find with it.

// doorbellEvery is how often this machine looks for machines of yours ringing.
const doorbellEvery = time.Minute

// doorbellNudge asks for a round now: a key chosen a moment ago should ring at once.
var doorbellNudge = make(chan struct{}, 1)

func nudgeDoorbell() {
	select {
	case doorbellNudge <- struct{}{}:
	default:
	}
}

// keepDoorbell rings while this machine knows no other machine of its user's, and answers every
// machine of theirs that rings, for as long as ctx lasts.
func keepDoorbell(ctx context.Context) {
	tick := time.NewTicker(doorbellEvery)
	defer tick.Stop()
	for {
		if svc := ringingWith(); svc != nil {
			doorbellRound(ctx, svc)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-doorbellNudge:
		}
	}
}

// ringingWith is the service that rings and answers, when this machine does either.
func ringingWith() *rendezvous.Service {
	if !user.Named() {
		return nil
	}
	rendezvousMu.Lock()
	defer rendezvousMu.Unlock()
	return rendezvousOn
}

// doorbellRound rings or stops ringing, and takes in whoever rang.
func doorbellRound(ctx context.Context, svc *rendezvous.Service) {
	pub, err := user.Public()
	if err != nil {
		return
	}
	pinned, err := book.Load()
	if err != nil {
		return
	}

	proof := ""
	if alone(pinned) {
		if badge, sig, err := user.Mine(time.Now()); err == nil {
			if proof, err = user.RingProof(badge, sig); err != nil {
				proof = ""
			}
		}
	}
	svc.Ring(pub.Marshal(), proof)

	now := time.Now()
	self, err := node.LocalID()
	if err != nil {
		return
	}
	for _, rang := range svc.Answer(ctx, pub.Marshal()) {
		badge, err := user.RungBadge(rang.Proof, pub, rang.ID.String(), now)
		if err != nil || rang.ID == self {
			continue
		}
		if user.Removed(rang.ID.String()) {
			log.Printf("%s rang holding your key, but was taken out of your machines: `drop machine add` puts it back", badge.Name)
			continue
		}
		if filed := fileMine(pinned, rang.ID, badge.Name); filed != "" {
			log.Printf("%s rang holding your key: it is one of your machines now, as %s", badge.Name, filed)
			nudgeMine()
		}
	}
}

// alone reports whether this machine knows no other machine of its user's.
func alone(pinned *book.Book) bool {
	me := myKey()
	for _, e := range pinned.All() {
		if me != "" && e.User == me && !user.Removed(e.ID.String()) {
			return false
		}
	}
	return true
}

// fileMine writes a machine down as one of this user's, under the secret the circle gives the two,
// and says what it was filed under: nothing when it was already known.
func fileMine(pinned *book.Book, id node.ID, name string) string {
	self, err := node.LocalID()
	if err != nil || id == self {
		return ""
	}
	circle, err := user.MakeCircle()
	if err != nil {
		return ""
	}
	filed := ""
	_ = pinned.Change(func() (bool, error) {
		if held, known := pinned.ByID(id); known && held.User == myKey() {
			return false, nil
		} else if known {
			pinned.Remove(held.Name)
		}
		filed = pinned.Join(fileName(name, id), id, user.PairSecret(circle, self.String(), id.String()), myKey())
		return true, nil
	})
	return filed
}
