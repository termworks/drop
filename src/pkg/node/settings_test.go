package node

import "testing"

// A device that changed networks is the ordinary case. Finding it again is the whole point of
// having paired with it, and that cannot happen unless this is on without being asked for.
func TestADeviceCanBeFoundAfterItMovesWithoutBeingConfigured(t *testing.T) {
	if !Rendezvous() {
		t.Fatal("a fresh install cannot find a device that moved")
	}
}

// And somebody who would rather be unreachable than announced can say so.
func TestItCanBeTurnedOff(t *testing.T) {
	t.Cleanup(func() { SetRendezvous(true) })

	SetRendezvous(false)
	if Rendezvous() {
		t.Fatal("turning it off did nothing")
	}
}
