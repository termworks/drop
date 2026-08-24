package cmd

import "testing"

// The pairing line between a local `drop pair` and the daemon is exactly three fields. Both ends
// are the same binary, so anything else is malformed rather than an older spelling to tolerate.
func TestAPairingOfferIsThreeFields(t *testing.T) {
	code, as, machine, err := offerAsked("abcd-efgh-ijkl bob person")
	if err != nil {
		t.Fatalf("a well-formed line was refused: %v", err)
	}
	if code != "abcd-efgh-ijkl" || as != "bob" || machine {
		t.Errorf("read %q, %q, %v", code, as, machine)
	}

	// A dash is a name that was not given, not a device called "-".
	if _, as, _, err = offerAsked("abcd-efgh-ijkl - machine"); err != nil || as != "" {
		t.Errorf("a dash came out as %q (%v)", as, err)
	}
	if _, _, machine, err = offerAsked("abcd-efgh-ijkl - machine"); err != nil || !machine {
		t.Errorf("machine came out %v (%v)", machine, err)
	}

	for _, bad := range []string{"", "abcd-efgh-ijkl", "abcd-efgh-ijkl bob"} {
		if _, _, _, err := offerAsked(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
