package seen

import (
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
)

func idFor(seed byte) node.ID {
	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

func TestAKnockIsRememberedWithWhatItWanted(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	at := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	if err := Knocked(idFor(1), "/work", "nothing here is shared with anyone", at); err != nil {
		t.Fatal(err)
	}

	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("remembered %d knocks", len(all))
	}
	if all[0].Asked != "/work" || all[0].Why == "" || !all[0].At.Equal(at) {
		t.Errorf("remembered %+v", all[0])
	}
}

// A device that dials in a loop is one entry, at the time it last tried -- not one entry per
// attempt, which would be a file that grows for as long as somebody keeps trying.
func TestOneDeviceIsOneEntry(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	first := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		when := first.Add(time.Duration(i) * time.Minute)
		if err := Knocked(idFor(1), "/work", "refused", when); err != nil {
			t.Fatal(err)
		}
	}

	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("five attempts made %d entries", len(all))
	}
	if !all[0].At.Equal(first.Add(4 * time.Minute)) {
		t.Errorf("kept %s, wanted the last attempt", all[0].At)
	}
}

// However many strangers dial, the file stays a fixed size, and what is dropped is the oldest.
func TestOnlySoManyAreKept(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	at := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	for i := 0; i < Most+10; i++ {
		when := at.Add(time.Duration(i) * time.Minute)
		if err := Knocked(idFor(byte(i)), "/work", "refused", when); err != nil {
			t.Fatal(err)
		}
	}

	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != Most {
		t.Fatalf("kept %d, wanted %d", len(all), Most)
	}

	// Most recent first, and the oldest are the ones that went.
	if !all[0].At.After(all[len(all)-1].At) {
		t.Error("the list is not newest first")
	}
	if all[len(all)-1].At.Before(at.Add(10 * time.Minute)) {
		t.Error("an older attempt survived a newer one")
	}
}

func TestAKnockCanBeForgotten(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if err := Knocked(idFor(1), "", "refused", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := Forget(idFor(1)); err != nil {
		t.Fatal(err)
	}

	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("still remembered: %+v", all)
	}
}
