package proto

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// echo is an archetype defined here and nowhere else. Handle has never heard of it.
type echo struct{ served chan string }

func (e echo) Name() string                            { return "echo" }
func (e echo) Version() int                            { return 1 }
func (e echo) Read(arch.Declared) (arch.Config, error) { return nil, nil }
func (e echo) Note(arch.Config) arch.Note              { return arch.Note{About: "says what it hears"} }

func (e echo) Serve(ctx context.Context, at arch.Session) error {
	e.served <- at.Path
	return at.Conn.WriteFrame(wire.KindItem, []byte(at.Rest))
}

// answering runs Handle over a pipe against one mount, and hands back the caller's end.
func answering(t *testing.T, m ns.Mount, known *arch.Registry) net.Conn {
	t.Helper()

	caller, server := net.Pipe()
	t.Cleanup(func() { caller.Close() })

	table := ns.NewTable()
	if err := table.Add(m); err != nil {
		t.Fatalf("adding %s: %v", m.Path, err)
	}

	go func() {
		defer server.Close()
		_ = Handle(t.Context(), stream{server}, who(3), Policy{
			Mounts:     table,
			Archetypes: known,
			Who:        func(node.ID, Badged) ns.Caller { return ns.Caller{ID: who(3).String(), Paired: true} },
			Allow:      func(node.ID, Opening) (bool, string) { return true, "" },
		})
	}()
	return caller
}

// stream is a pipe with the deadline a session stream is expected to have.
type stream struct{ net.Conn }

func (s stream) SetReadDeadline(t time.Time) error { return s.Conn.SetReadDeadline(t) }

// The whole point of the boundary: an archetype nothing in this package knows about is registered,
// mounted, opened and served, and Handle gains no case for it.
func TestHandleServesAnArchetypeItHasNeverHeardOf(t *testing.T) {
	served := make(chan string, 1)
	known := arch.NewRegistry()
	known.Register(echo{served: served})

	mount := ns.Mount{Path: "/say", Archetype: "echo", Access: ns.Access{AnyPaired: true}}
	caller := answering(t, mount, known)

	conn, err := Open(caller, "/say/back", "echo", 0, "", "tester")
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if at := <-served; at != "/say" {
		t.Errorf("served %q, want the mount", at)
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		t.Fatalf("reading what it said: %v", err)
	}
	if kind != wire.KindItem || string(body) != "/back" {
		t.Errorf("it said frame kind %d, %q", kind, body)
	}
}

// Naming the wrong thing is answered plainly, because it is almost always a path somebody typed
// from memory rather than an attack.
func TestHandleRefusesAnOpenThatNamesAnotherArchetype(t *testing.T) {
	known := arch.NewRegistry()
	known.Register(echo{served: make(chan string, 1)})

	mount := ns.Mount{Path: "/say", Archetype: "echo", Access: ns.Access{AnyPaired: true}}
	caller := answering(t, mount, known)

	_, err := Open(caller, "/say", "chat", 0, "", "tester")
	if err == nil {
		t.Fatal("a session opened as the wrong archetype was taken")
	}
	if !WasDeclined(err) || !strings.Contains(err.Error(), "/say is a echo namespace") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// Naming nothing asks for whatever is there, which is what somebody typing a path is doing.
func TestHandleTakesAnOpenThatNamesNothing(t *testing.T) {
	served := make(chan string, 1)
	known := arch.NewRegistry()
	known.Register(echo{served: served})

	mount := ns.Mount{Path: "/say", Archetype: "echo", Access: ns.Access{AnyPaired: true}}
	caller := answering(t, mount, known)

	if _, err := Open(caller, "/say", "", 0, "", "tester"); err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if at := <-served; at != "/say" {
		t.Errorf("served %q", at)
	}
}

// A mount whose archetype this build does not register is refused with what it does have, rather
// than with silence on an accepted stream.
func TestHandleRefusesAMountNothingCanServe(t *testing.T) {
	known := arch.NewRegistry()
	known.Register(echo{served: make(chan string, 1)})

	mount := ns.Mount{Path: "/film", Archetype: "camera", Access: ns.Access{AnyPaired: true}}
	caller := answering(t, mount, known)

	_, err := Open(caller, "/film", "camera", 0, "", "tester")
	if err == nil {
		t.Fatal("a namespace nothing can serve was opened")
	}
	if !strings.Contains(err.Error(), "camera") || !strings.Contains(err.Error(), "echo") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}
