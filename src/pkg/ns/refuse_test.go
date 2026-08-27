package ns

import "testing"

// A machine that has been refused stays refused, whatever else it says about itself.
//
// A device paired on its own is filed under a machine name and refused under that name — that is
// what the interface writes. But a badge naming a person this machine already knows fills in the
// caller's person, and admitting matches a bare name only when there is no person. So the refusal
// stopped applying the moment the device attached a badge, which it does on every connection. The
// interface went on drawing it as refused.
func TestARefusedMachineStaysRefusedWhenItNamesAPerson(t *testing.T) {
	rule := Access{AnyPaired: true, AnyTrusted: true, Refused: []string{"buildbox"}}

	for what, who := range map[string]Caller{
		"no person at all":  {ID: "abc", Name: "buildbox", Paired: true, Trusted: true},
		"a person it knows": {ID: "abc", Name: "buildbox", UserName: "bob", Paired: true, Trusted: true},
		"one of my own":     {ID: "abc", Name: "buildbox", UserName: "me", Paired: true, Trusted: true},
	} {
		if ok, _ := rule.Admits(who); ok {
			t.Errorf("a machine refused by name was admitted when it came with %s", what)
		}
	}

	// A machine that was never refused is still let in, so the check refuses one thing and not
	// everything.
	other := Caller{ID: "def", Name: "laptop", UserName: "bob", Paired: true, Trusted: true}
	if ok, why := rule.Admits(other); !ok {
		t.Fatalf("a machine nobody refused was turned away: %s", why)
	}
}

// And a refusal spelt as a person still refuses every machine of theirs.
func TestARefusedPersonIsRefusedOnEveryMachine(t *testing.T) {
	rule := Access{AnyPaired: true, Refused: []string{"bob"}}

	for _, machine := range []string{"laptop", "phone", ""} {
		who := Caller{ID: "abc", Name: machine, UserName: "bob", Paired: true}
		if ok, _ := rule.Admits(who); ok {
			t.Errorf("bob was admitted on %q after being refused", machine)
		}
	}
}
