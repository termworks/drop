package proto

import (
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/metal"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/plate"
)

// A plate has to survive being written into an opening and read back out, or none of the rest of
// it matters.
func TestAPlateRidesInTheOpening(t *testing.T) {
	if !metal.Read().Held() {
		t.Skip("this machine says nothing about itself, so it can stamp nothing")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	stamp, sig, err := plate.Sign(time.Now())
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}

	open := Opening{Path: "/notes", Plate: stamp.Bytes(), Stamped: sig}
	back, err := decodeOpen(open.encode())
	if err != nil {
		t.Fatalf("decodeOpen(): %v", err)
	}
	if string(back.Plate) != string(open.Plate) || string(back.Stamped) != string(open.Stamped) {
		t.Fatal("the plate did not survive the trip")
	}

	// And it checks out against the endpoint the transport proved, which is what makes it worth
	// carrying at all.
	here, err := node.LocalID()
	if err != nil {
		t.Fatal(err)
	}
	on := stood(here, back)
	if !on.Shown() {
		t.Fatal("a plate this machine signed did not check out for this machine")
	}
	if on.Machine != stamp.Machine.String() || on.Whose != metal.Whose() {
		t.Fatalf("the plate came back as %+v", on)
	}
}

// A plate is for one endpoint. Replaying somebody else's onto your own connection must get nothing.
func TestAPlateForAnotherEndpointIsNotBelieved(t *testing.T) {
	if !metal.Read().Held() {
		t.Skip("this machine says nothing about itself, so it can stamp nothing")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	stamp, sig, err := plate.Sign(time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if on := stood(idFrom(9), Opening{Plate: stamp.Bytes(), Stamped: sig}); on.Shown() {
		t.Fatalf("a plate for another endpoint was believed: %+v", on)
	}
}

// Nothing, and rubbish, are both a caller nothing is known about rather than an error.
func TestRubbishIsNotAPlate(t *testing.T) {
	here, err := node.LocalID()
	if err != nil {
		t.Fatal(err)
	}

	for what, open := range map[string]Opening{
		"nothing at all":   {},
		"no signature":     {Plate: []byte("drop-plate/1\n")},
		"no plate":         {Stamped: make([]byte, 64)},
		"a bent plate":     {Plate: []byte("nonsense"), Stamped: make([]byte, 64)},
		"a bent signature": {Plate: []byte("drop-plate/1\n"), Stamped: []byte("short")},
	} {
		if on := stood(here, open); on.Shown() {
			t.Errorf("%s was read as a plate: %+v", what, on)
		}
	}
}
