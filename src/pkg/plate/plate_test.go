package plate

import (
	"strings"
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/metal"
	"github.com/bresilla/drop/src/pkg/node"
)

// A machine that will not name itself cannot vouch for anything, and every test here needs one
// that will.
func onRealMetal(t *testing.T) {
	t.Helper()

	if !metal.Read().Held() {
		t.Skip("this machine says nothing about itself, so it can stamp nothing")
	}
}

func at(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
}

// The ordinary case: a machine stamps the drop running on it, and somebody who was not there can
// tell what it said.
func TestAMachineStampsTheDropRunningOnIt(t *testing.T) {
	onRealMetal(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := at(t)

	stamp, sig, err := Sign(now)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}

	got, err := Read(stamp.Bytes(), sig, now)
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}

	here, err := node.LocalID()
	if err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != here {
		t.Fatalf("the stamp is for %s and this drop is %s", node.Brief(got.Endpoint), node.Brief(here))
	}
	if got.Whose != metal.Whose() {
		t.Fatalf("the stamp says the account is %q and it is %q", got.Whose, metal.Whose())
	}

	// The machine it names is the machine, not the drop: those are different keys on purpose.
	machine, err := MachineID()
	if err != nil {
		t.Fatal(err)
	}
	if got.Machine != machine {
		t.Fatalf("the stamp names machine %s and this machine is %s", node.Brief(got.Machine), node.Brief(machine))
	}
	if got.Machine == got.Endpoint {
		t.Fatal("the machine and the drop running on it came out as one key")
	}
}

// Two people with accounts on one machine: two drops, two names, one machine — which is the whole
// reason a stamp exists.
func TestTwoAccountsOnOneMachineStampToTheSameMachine(t *testing.T) {
	onRealMetal(t)

	mark := metal.Read()
	one, err := mark.Seed("alice")
	if err != nil {
		t.Fatal(err)
	}
	two, err := mark.Seed("bob")
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("two accounts on one machine derived one drop key")
	}

	// And both of them speak for the same machine, because that is what makes it one machine.
	machine, err := MachineID()
	if err != nil {
		t.Fatal(err)
	}
	if machine.IsZero() {
		t.Fatal("this machine has no key of its own")
	}
	if node.From(one) == machine || node.From(two) == machine {
		t.Fatal("somebody's drop key is also the machine key")
	}
}

// A stamp that ran out is not a stamp.
func TestAStampThatRanOutIsRefused(t *testing.T) {
	onRealMetal(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := at(t)

	stamp, sig, err := Sign(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(stamp.Bytes(), sig, now.Add(Lasts+time.Second)); err == nil {
		t.Fatal("a stamp was believed after it ran out")
	}
}

// Changing a byte of what was signed must not survive, and neither must changing who signed it.
func TestAStampCannotBeEditedAfterItIsSigned(t *testing.T) {
	onRealMetal(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := at(t)

	stamp, sig, err := Sign(now)
	if err != nil {
		t.Fatal(err)
	}

	// Somebody else's endpoint, put into a stamp this machine signed.
	other, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	moved := stamp
	moved.Endpoint = other.Public().EndpointID()
	if _, err := Read(moved.Bytes(), sig, now); err == nil {
		t.Fatal("a stamp was moved onto another endpoint and still checked out")
	}

	// The account changed, which is how one person on a machine would become another.
	renamed := stamp
	renamed.Whose = "root"
	if _, err := Read(renamed.Bytes(), sig, now); err == nil {
		t.Fatal("a stamp was moved onto another account and still checked out")
	}

	// A stamp claiming to be from a machine that did not sign it.
	claimed := stamp
	claimed.Machine = other.Public().EndpointID()
	if _, err := Read(claimed.Bytes(), sig, now); err == nil {
		t.Fatal("a stamp naming a machine that did not sign it checked out")
	}

	// And a signature that is simply wrong.
	bent := append([]byte(nil), sig...)
	bent[0] ^= 0xff
	if _, err := Read(stamp.Bytes(), bent, now); err == nil {
		t.Fatal("a stamp with a bent signature checked out")
	}
}

// What is not a stamp is refused rather than half-read.
func TestWhatIsNotAStampIsRefused(t *testing.T) {
	now := at(t)

	for _, bad := range []string{
		"",
		"drop-plate/2\n",
		"drop-badge/1\nmachine x\n",
		"drop-plate/1\nmachine\n",
		"drop-plate/1\nwhose alice\n",
		"drop-plate/1\nmachine aaa\nendpoint bbb\nwhose alice\nuntil nope\n",
		"drop-plate/1\nwhose alice\nwhose bob\n",
		"drop-plate/1\nmachine aaa\nsomething else\n",
	} {
		if _, err := Read([]byte(bad), make([]byte, key.SignatureSize), now); err == nil {
			t.Errorf("%q was read as a stamp", bad)
		}
	}
}

// A stamp written any way but the one way is refused, because the signature covers bytes and a
// reader that forgives a line can be shown different bytes to the ones it checked.
func TestAStampWrittenAnotherWayIsRefused(t *testing.T) {
	onRealMetal(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := at(t)

	stamp, sig, err := Sign(now)
	if err != nil {
		t.Fatal(err)
	}

	// The same fields, in another order: it says the same thing and is not the same bytes.
	shuffled := strings.Split(strings.TrimRight(string(stamp.Bytes()), "\n"), "\n")
	shuffled[1], shuffled[2] = shuffled[2], shuffled[1]
	if _, err := Read([]byte(strings.Join(shuffled, "\n")+"\n"), sig, now); err == nil {
		t.Fatal("a stamp with its lines shuffled checked out")
	}

	// A line nobody knows, added to something that was signed without it.
	extra := string(stamp.Bytes()) + "trusted yes\n"
	if _, err := Read([]byte(extra), sig, now); err == nil {
		t.Fatal("a stamp with a line added checked out")
	}
}
