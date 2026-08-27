package proto

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/passwd"
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
			Who:        func(node.ID, Badged, Stood) ns.Caller { return ns.Caller{ID: who(3).String(), Paired: true} },
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

// handling runs Handle over a pipe against one table, and hands back the caller's end.
func handling(t *testing.T, from node.ID, table *ns.Table, policy Policy) net.Conn {
	t.Helper()

	caller, server := net.Pipe()
	t.Cleanup(func() { caller.Close() })

	policy.Mounts = table
	go func() {
		defer server.Close()
		_ = Handle(t.Context(), stream{server}, from, policy)
	}()
	return caller
}

// An ask carries what somebody said about why they want a path. Reading it as a password hands a
// stranger an argon2 hash for every ask, and lets a sentence open a path guarded by a word.
func TestAnAskDoesNotSpendAPassword(t *testing.T) {
	hash, err := passwd.Hash("let me in")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}

	table := served(t, ns.Mount{Path: "/handoff", Archetype: "share", Access: ns.Access{Password: hash}})
	heard := make(chan Asker, 1)
	caller := handling(t, who(4), table, Policy{Asked: func(a Asker) error { heard <- a; return nil }})

	if err := Ask(t.Context(), caller, "/handoff", "let me in", "tester"); err == nil {
		t.Fatal("an ask was answered for a path only a password reaches")
	}
	select {
	case a := <-heard:
		t.Fatalf("the request was taken: %+v", a)
	default:
	}
}

// What a refusal says is a map of this machine drawn for whoever asks. A caller that may not be
// here learns that and no more: which paths exist, and which of them hold others, is the thing a
// listing is filtered to keep back.
func TestARefusalDoesNotSayWhatIsThere(t *testing.T) {
	table := served(t,
		ns.Mount{Path: "/work", Archetype: "echo", Access: ns.Access{Named: []string{"bob"}}},
		ns.Mount{Path: "/private", Access: ns.Access{Named: []string{"bob"}}},
		ns.Mount{Path: "/private/term", Archetype: "echo"},
	)

	var said []string
	for _, path := range []string{"/nowhere", "/private", "/work"} {
		caller := handling(t, who(5), table, Policy{Allow: func(node.ID, Opening) (bool, string) { return true, "" }})

		_, err := Open(caller, path, "", 0, "", "tester")
		if err == nil {
			t.Fatalf("a stranger opened %s", path)
		}
		said = append(said, strings.TrimPrefix(err.Error(), "declined: "+path))
	}

	for i, text := range said {
		if text != said[0] {
			t.Errorf("probe %d was answered %q, the first with %q", i, text, said[0])
		}
	}
	if !strings.Contains(said[0], "not shared with you") {
		t.Errorf("a refusal that does not say what went wrong: %q", said[0])
	}
}

// A path off the wire is a thousand arbitrary bytes that end up written down and drawn in the
// interface, where escape sequences are what somebody else's terminal does rather than text.
func TestARefusedPathIsWrittenDownAsThisNodeSpellsIt(t *testing.T) {
	table := served(t, ns.Mount{Path: "/work", Archetype: "echo", Access: ns.Access{AnyPaired: true}})

	noted := make(chan string, 1)
	caller := handling(t, who(6), table, Policy{
		Refused: func(_ node.ID, asked, why string) { noted <- asked + " " + why },
	})

	if _, err := Open(caller, "\x1b[2J\x1b]0;pwned\x07work", "", 0, "", "tester"); err == nil {
		t.Fatal("a path nothing can spell was served")
	}
	if got := <-noted; strings.ContainsRune(got, 0x1b) {
		t.Fatalf("what was written down carries the escapes it arrived with: %q", got)
	}
}

// A password path is reachable by anybody who knows this device's id, and every guess is 64 MiB and
// three passes spent here rather than there. So a peer gets a handful and then waits.
func TestAStrangerCannotGuessWithoutLimit(t *testing.T) {
	hash, err := passwd.Hash("let me in")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	table := served(t, ns.Mount{Path: "/handoff", Archetype: "echo", Access: ns.Access{Password: hash}})

	stranger := who(7)
	for i := range mostGuesses {
		caller := handling(t, stranger, table, Policy{})
		if _, err := Open(caller, "/handoff", "", 0, fmt.Sprintf("guess %d", i), "tester"); err == nil {
			t.Fatalf("guess %d opened the path", i)
		}
	}

	caller := handling(t, stranger, table, Policy{})
	_, err = Open(caller, "/handoff", "", 0, "one more", "tester")
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("a guess past the allowance was answered with %v", err)
	}

	// One peer's guessing must not be another's problem.
	other := handling(t, who(8), table, Policy{})
	if _, err := Open(other, "/handoff", "", 0, "wrong as well", "tester"); err == nil {
		t.Fatal("a wrong password opened the path")
	} else if strings.Contains(err.Error(), "too many") {
		t.Fatalf("a peer that had guessed nothing was refused: %v", err)
	}
}

// One open, one guess. A refused open used to put the same wrong word to the same rule twice: once
// to be turned away, and again on the way to saying the path may be asked for.
func TestARefusedOpenCostsOneGuess(t *testing.T) {
	hash, err := passwd.Hash("let me in")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}

	table := served(t, ns.Mount{
		Path:      "/handoff",
		Archetype: "echo",
		Access:    ns.Access{Password: hash, AnyVisible: true},
	})
	caller := handling(t, who(9), table, Policy{
		Who: func(from node.ID, _ Badged, _ Stood) ns.Caller { return ns.Caller{ID: from.String(), Paired: true} },
	})

	// Counted rather than timed: a guess is expensive on purpose, and how many were run is the
	// question. A stopwatch answers it only on a machine doing nothing else.
	before := passwd.Spent()

	_, err = Open(caller, "/handoff", "", 0, "nope", "tester")
	if err == nil || !strings.Contains(err.Error(), "you may ask for it") {
		t.Fatalf("a wrong password was answered with %v", err)
	}

	// The rule is asked twice on the way through — once to admit, once to say the path may be
	// asked for — and the guess must be hashed for only the first of them.
	if paid := passwd.Spent() - before; paid != 1 {
		t.Errorf("one refused open paid for %d guesses, want 1", paid)
	}
}
