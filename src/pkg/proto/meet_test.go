package proto

import (
	"net"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// held is one namespace several machines hold, as a config would declare it.
var held = ns.Shared{Creator: "ssh-ed25519 AAAA alice\n", At: "/notes", Nonce: "cafe"}

// catching runs Handle over a pipe with somewhere for a catch-up to land.
func catching(t *testing.T, m ns.Mount, met func(Meeting) error) net.Conn {
	t.Helper()

	caller, server := net.Pipe()
	t.Cleanup(func() { caller.Close() })

	table := ns.NewTable()
	if err := table.Add(m); err != nil {
		t.Fatalf("adding %s: %v", m.Path, err)
	}

	go func() {
		defer server.Close()
		_ = Handle(t.Context(), stream{server}, who(4), Policy{
			Mounts:     table,
			Archetypes: arch.NewRegistry(),
			Who:        func(node.ID, Badged) ns.Caller { return ns.Caller{ID: who(4).String(), Paired: true} },
			Allow:      func(node.ID, Opening) (bool, string) { return true, "" },
			Met:        met,
		})
	}()
	return caller
}

// A catch-up is not an open. It is answered before the archetype is looked at, because what is said
// afterwards is heads and changes rather than anything an archetype would recognise.
func TestAMeetingIsHandedOverWithoutTheArchetype(t *testing.T) {
	arrived := make(chan Meeting, 1)
	mount := ns.Mount{Path: "/notes", Archetype: "chat", Access: ns.Access{AnyPaired: true}, Shared: held}

	caller := catching(t, mount, func(m Meeting) error {
		arrived <- m
		return m.Conn.WriteFrame(wire.KindEnd, nil)
	})

	conn, err := Meet(caller, "/notes", "tester")
	if err != nil {
		t.Fatalf("Meet(): %v", err)
	}

	m := <-arrived
	if m.Mount.Shared.ID() != held.ID() {
		t.Fatalf("the meeting is about %s, want %s", m.Mount.Shared.ID(), held.ID())
	}
	if !m.Who.Paired {
		t.Fatal("the meeting says nothing about who is on the other end")
	}

	if kind, _, err := conn.ReadFrame(); err != nil || kind != wire.KindEnd {
		t.Fatalf("read frame kind %d: %v", kind, err)
	}
}

// A namespace this machine holds alone has no history anybody else is part of, and saying so is
// better than a session that goes quiet.
func TestAMeetingOnANamespaceNobodyElseHoldsIsRefused(t *testing.T) {
	mount := ns.Mount{Path: "/chat", Archetype: "chat", Access: ns.Access{AnyPaired: true}}

	caller := catching(t, mount, func(Meeting) error { return nil })

	_, err := Meet(caller, "/chat", "tester")
	if !WasDeclined(err) {
		t.Fatalf("Meet() = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "anybody else holds") {
		t.Fatalf("Meet() = %v", err)
	}
}

// A process that keeps no history says so rather than accepting and then saying nothing.
func TestAMeetingIsRefusedWhereNothingKeepsAHistory(t *testing.T) {
	mount := ns.Mount{Path: "/notes", Archetype: "chat", Access: ns.Access{AnyPaired: true}, Shared: held}

	caller := catching(t, mount, nil)

	if _, err := Meet(caller, "/notes", "tester"); !WasDeclined(err) {
		t.Fatalf("Meet() = %v, want a refusal", err)
	}
}

// A caller the rule does not admit is refused a catch-up exactly as it is refused an open: reaching
// it is changing it.
func TestAMeetingFromSomebodyNotAdmittedIsRefused(t *testing.T) {
	mount := ns.Mount{Path: "/notes", Archetype: "chat", Access: ns.Access{Named: []string{"nobody"}}, Shared: held}

	caller := catching(t, mount, func(Meeting) error { return nil })

	if _, err := Meet(caller, "/notes", "tester"); !WasDeclined(err) {
		t.Fatalf("Meet() = %v, want a refusal", err)
	}
}
