package cmd

import (
	"strings"
	"testing"
)

// A pairing ticket handed to the place that takes badges says what it is, rather than failing to
// decode as one: the two are scanned off the same kind of screen.
func TestAPairingCodeIsNotTakenForABadge(t *testing.T) {
	for _, code := range []string{
		"9363f77d1b4e45bdb8df0ed98b6f9c8d7b1a2f3e4d5c6b7a8f9e0d1c2b3a4f5e#qxwo-e62y",
		"drop://pair/9363f77d1b4e45bdb8df0ed98b6f9c8d7b1a2f3e4d5c6b7a8f9e0d1c2b3a4f5e#qxwo-e62y",
	} {
		_, err := TakeCode(code)
		if err == nil || !strings.Contains(err.Error(), "pairing code") {
			t.Fatalf("TakeCode(%q) = %v, want it called a pairing code", code, err)
		}
	}
}
