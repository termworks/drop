package cmd

import (
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/ns"
)

// There is no list of who holds a namespace: the rule is the list. A person the rule does not name
// is one whose changes are refused, and everything made after one of theirs with them — which is
// the one thing about holding a thing together that the table itself cannot show.
func TestTheListingSaysWhatTheRuleMeansForAThingHeldTogether(t *testing.T) {
	mounts := []ns.Mount{
		{Path: "/term", Archetype: "tty"},
		{Path: "/notes", Archetype: "chat", Shared: ns.Shared{Creator: "ssh-ed25519 AAAA alice\n", At: "/notes"}},
	}

	said := membership(mounts)
	if !strings.Contains(said, "refused") {
		t.Fatalf("membership() = %q, want it to say what happens to a change by anybody else", said)
	}
}

// Nothing to say where nothing is held together.
func TestTheListingSaysNothingAboutSharingWhereThereIsNone(t *testing.T) {
	mounts := []ns.Mount{{Path: "/term", Archetype: "tty"}}

	if said := membership(mounts); said != "" {
		t.Fatalf("membership() = %q, want nothing", said)
	}
}
