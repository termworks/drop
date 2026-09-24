package user

import (
	"testing"
	"time"
)

// The later of two marks for one machine is the one that stands, whichever arrives first.
func TestTheLaterMarkStands(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := time.Unix(1000, 0)

	if err := Remove("phone", now); err != nil {
		t.Fatal(err)
	}
	if !Removed("phone") {
		t.Fatal("a machine taken out does not read as taken out")
	}

	gone, err := Merge(map[string]Mark{"phone": {At: 900}})
	if err != nil || len(gone) != 0 || !Removed("phone") {
		t.Fatalf("an older mark putting it back stood (%v, %v)", gone, err)
	}

	if err := Restore("phone", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if Removed("phone") {
		t.Fatal("a machine put back still reads as taken out")
	}

	gone, err = Merge(map[string]Mark{"phone": {At: 2000, Gone: true}, "laptop": {At: 5, Gone: true}})
	if err != nil || len(gone) != 2 || !Removed("phone") || !Removed("laptop") {
		t.Fatalf("newer marks taking machines out did not stand (%v, %v)", gone, err)
	}
}

// Putting back a machine that was never taken out writes nothing.
func TestRestoringAMachineNeverTakenOutWritesNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Restore("phone", time.Now()); err != nil {
		t.Fatal(err)
	}
	held, err := Marks()
	if err != nil || len(held) != 0 {
		t.Fatalf("restoring nothing left %v (%v)", held, err)
	}
}
