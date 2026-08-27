package plate

import (
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
)

// The ordinary case: a machine says what it became, and somebody holding only the old name can
// check it. Nothing new has to be trusted, which is what makes this usable at all.
func TestAMachineSaysWhatItBecameAndTheOldNameProvesIt(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	fresh, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	to := fresh.Public().EndpointID()

	over, sig, err := Hand(to, "bresilla", now)
	if err != nil {
		t.Fatalf("Hand(): %v", err)
	}

	got, err := Took(over.Bytes(), sig, now)
	if err != nil {
		t.Fatalf("Took(): %v", err)
	}

	was, err := node.LocalID()
	if err != nil {
		t.Fatal(err)
	}
	if got.Was != was {
		t.Fatalf("the handover is from %s and this machine is %s", node.Brief(got.Was), node.Brief(was))
	}
	if got.Now != to {
		t.Fatalf("the handover points at %s, want %s", node.Brief(got.Now), node.Brief(to))
	}
}

// A machine cannot hand itself over to itself, and cannot hand over to nothing.
func TestAMachineCannotHandOverToItselfOrToNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	was, err := node.LocalID()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Hand(was, "bresilla", now); err == nil {
		t.Fatal("a machine handed itself over to itself")
	}
	if _, _, err := Hand(node.ID{}, "bresilla", now); err == nil {
		t.Fatal("a machine handed itself over to nothing")
	}
}

// A handover is dated. One kept and replayed later would move a machine that has since moved on.
func TestAHandoverRunsOut(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	fresh, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	over, sig, err := Hand(fresh.Public().EndpointID(), "bresilla", now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Took(over.Bytes(), sig, now.Add(Moving+time.Second)); err == nil {
		t.Fatal("a handover was acted on after it ran out")
	}
}

// The one thing that must not be possible: pointing somebody else's machine at yours.
func TestAHandoverCannotBeWrittenForAMachineYouAreNot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	mine, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	over, sig, err := Hand(mine.Public().EndpointID(), "bresilla", now)
	if err != nil {
		t.Fatal(err)
	}

	// Somebody else's machine, put in as the one being handed over.
	theirs, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	stolen := over
	stolen.Was = theirs.Public().EndpointID()
	if _, err := Took(stolen.Bytes(), sig, now); err == nil {
		t.Fatal("a handover was written for a machine that did not sign it")
	}

	// Repointing a real handover at somewhere else.
	moved := over
	moved.Now = theirs.Public().EndpointID()
	if _, err := Took(moved.Bytes(), sig, now); err == nil {
		t.Fatal("a handover was repointed after it was signed")
	}

	// And onto another account on the same machine.
	renamed := over
	renamed.Whose = "root"
	if _, err := Took(renamed.Bytes(), sig, now); err == nil {
		t.Fatal("a handover was moved onto another account")
	}
}

// What is not a handover is refused rather than half-read, and a stamp is not a handover.
func TestWhatIsNotAHandoverIsRefused(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	for _, bad := range []string{
		"",
		"drop-plate/1\nmachine aaa\n",
		"drop-handover/2\n",
		"drop-handover/1\nwas aaa\n",
		"drop-handover/1\nwas aaa\nnow aaa\nwhose me\nuntil nope\n",
		"drop-handover/1\nwas aaa\nwas bbb\n",
		"drop-handover/1\nwas aaa\nnow bbb\nwhose me\nuntil 2026-08-27T12:00:00Z\nand more\n",
	} {
		if _, err := Took([]byte(bad), make([]byte, key.SignatureSize), now); err == nil {
			t.Errorf("%q was read as a handover", bad)
		}
	}
}
