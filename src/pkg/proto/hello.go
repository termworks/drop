package proto

import (
	"fmt"
	"io"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/plain"
	"github.com/bresilla/drop/src/pkg/wire"
)

// MaxServed bounds how many namespaces a node will describe or believe. A list is a convenience,
// not a thing worth allocating without limit for.
const MaxServed = 256

// Served is one namespace a node offers.
//
// What is said about it comes from the archetype it belongs to, so a kind of path invented next
// week describes itself here without this frame learning a word about it.
type Served struct {
	Path string
	// Archetype is what is there, by name, and Version which revision of it. Both empty for a
	// branch, which is a path that holds others and serves nothing.
	Archetype string
	Version   int
	// Shape is another archetype whose protocol this one speaks, and is empty for one that
	// invented its own. It is what a caller that has never heard of the archetype falls back to,
	// and the whole of what it needs: what to say down the stream.
	Shape string
	// Writable says the far end may put something into it.
	Writable bool
	// Locked says this path can be seen but not opened. It is here to be asked for.
	Locked bool
	// About is what this kind of path is for, in the words of whoever wrote it.
	About string
	// Shared says several machines hold this one namespace, and what they all call it. It is what
	// a joiner needs and the whole of it: the name is worked out from these three facts rather
	// than taken on trust, so nothing else has to travel for one machine to hold what another
	// holds.
	Shared ns.Shared
	// Holders is who else holds it, by the key they sign with. Absent for a path the caller may
	// see and not open: who is inside is for the people inside.
	Holders []string
}

// MaxHolders bounds how many people a node will name as holding one namespace, so a listing stays
// a listing.
const MaxHolders = 64

// Hello is what a node answers with when asked what it calls itself. Self-declared, so it names a
// peer but never authenticates one; the endpoint id does that.
//
// Serves is empty unless the caller is paired. What a device offers — that it has a terminal, where
// it files things — is not something to tell a stranger who merely dialled.
type Hello struct {
	Name    string
	Version string
	Serves  []Served
}

// encode writes what this node says it offers, cut to what the far end will read.
//
// Both lists are cut here rather than left to the reader, which refuses the whole message: a node
// with one crowded namespace would otherwise be a node nobody can list at all, and the crowding is
// its own doing rather than anything a caller did.
func (h Hello) encode() []byte {
	w := wire.NewWriter()
	w.String(h.Name)
	w.String(h.Version)

	serves := h.Serves
	if len(serves) > MaxServed {
		serves = serves[:MaxServed]
	}

	w.Uint(uint64(len(serves)))
	for _, s := range serves {
		holders := s.Holders
		if len(holders) > MaxHolders {
			holders = holders[:MaxHolders]
		}
		w.String(s.Path)
		w.String(s.Archetype)
		w.String(s.Shape)
		w.Uint(uint64(s.Version))
		w.Bool(s.Writable)
		w.Bool(s.Locked)
		w.String(s.About)
		w.String(s.Shared.Creator)
		w.String(s.Shared.At)
		w.String(s.Shared.Nonce)
		w.Uint(uint64(len(holders)))
		for _, key := range holders {
			w.String(key)
		}
	}
	return w.Body()
}

func decodeHello(body []byte) (Hello, error) {
	var out Hello

	r := wire.NewReader(body)
	name, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	version, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	// Everything a peer says about itself is bytes it chose and drop prints. Made safe here, at the
	// one place it arrives, rather than at each of the places it is shown — a listing that forgot
	// is a listing a peer can write on.
	out.Name, out.Version = plain.Line(name), plain.Line(version)

	count, err := r.Uint()
	if err != nil {
		return out, err
	}
	if count > MaxServed {
		return out, fmt.Errorf("a node claimed %d namespaces, which is more than %d", count, MaxServed)
	}

	for range count {
		path, err := r.String(wire.MaxString)
		if err != nil {
			return out, err
		}
		archetype, err := r.String(256)
		if err != nil {
			return out, err
		}
		shape, err := r.String(256)
		if err != nil {
			return out, err
		}
		version, err := r.Uint()
		if err != nil {
			return out, err
		}
		writable, err := r.Bool()
		if err != nil {
			return out, err
		}
		locked, err := r.Bool()
		if err != nil {
			return out, err
		}
		about, err := r.String(wire.MaxString)
		if err != nil {
			return out, err
		}
		shared, err := decodeShared(r)
		if err != nil {
			return out, err
		}
		holders, err := decodeHolders(r)
		if err != nil {
			return out, err
		}
		out.Serves = append(out.Serves, Served{
			Path:      plain.Text(path, MaxPathShown),
			Archetype: plain.Line(archetype),
			Shape:     plain.Line(shape),
			Version:   int(version),
			Writable:  writable,
			Locked:    locked,
			About:     plain.Line(about),
			Shared:    shared,
			Holders:   holders,
		})
	}
	return out, nil
}

// decodeShared reads what a namespace several machines hold is called.
func decodeShared(r *wire.Reader) (ns.Shared, error) {
	var out ns.Shared

	creator, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	at, err := r.String(1024)
	if err != nil {
		return out, err
	}
	nonce, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	out.Creator, out.At, out.Nonce = creator, at, nonce
	return out, nil
}

func decodeHolders(r *wire.Reader) ([]string, error) {
	count, err := r.Uint()
	if err != nil {
		return nil, err
	}
	if count > MaxHolders {
		return nil, fmt.Errorf("a node named %d people holding one namespace, which is more than %d", count, MaxHolders)
	}
	if count == 0 {
		return nil, nil
	}

	out := make([]string, 0, count)
	for range count {
		key, err := r.String(MaxHolder)
		if err != nil {
			return nil, err
		}
		// Named by the far end and printed here, the same as everything else it says about itself.
		// A key is printable already, so this changes nothing about a real one.
		out = append(out, plain.Line(key))
	}
	return out, nil
}

// Describe lists what one caller may reach, in the order a person would read it.
//
// Filtered rather than gated: a path the caller cannot open is absent, not marked refused. A
// listing that showed the whole tree would tell someone which machine has a terminal worth
// attacking, which is exactly what they should not learn from asking politely.
//
// A path guarded by a password is therefore never listed — nobody offers a secret to ask what
// exists — so whoever is given one needs the path as well as the word.
func Describe(table *ns.Table, known *arch.Registry, caller ns.Caller) []Served {
	if table == nil {
		return nil
	}

	var out []Served
	for _, m := range table.All() {
		open, _ := table.Admits(m.Path, caller)
		if !open && !table.Sees(m.Path, caller) {
			continue
		}

		said := Served{Path: m.Path, Archetype: m.Archetype, Version: m.Version, Locked: !open}
		// That several machines hold this is told to the people who may hold it, and not to
		// somebody who can see the path and cannot open it: what a namespace is called is what
		// joining it takes.
		if open {
			said.Shared = m.Shared
		}
		if m.Branch() {
			said.About = "holds other paths, serves nothing"
			out = append(out, said)
			continue
		}
		// What may be said about it comes from the archetype, including which revision of itself
		// will answer when a mount pinned none.
		if answers, ok := lookup(known, m); ok {
			note := answers.Note(m.Config)
			said.Version = answers.Version()
			said.Writable, said.About, said.Shape = open && note.Writable, note.About, note.Shape
		}
		out = append(out, said)
	}
	return out
}

// lookup finds what answers for a mount, and nothing at all when this build has no idea.
func lookup(known *arch.Registry, m ns.Mount) (arch.Archetype, bool) {
	if known == nil {
		return nil, false
	}
	return known.Lookup(m.Archetype, m.Version)
}

// Moved is told when the ask carried word that a machine became this caller, once that checks out:
// a hello is the first thing many peers hear from a machine that moved, so the news travels here as
// well as on an open. Nil ignores it.
//
// AnswerHello reads the ask and answers it. Self is built from the badge the ask carried, because
// what this node is willing to say it serves depends on who is asking.
//
// Hello is answered to anybody who dials, so the ask is bounded the way a session's open is: a peer
// that says half a frame and then nothing otherwise holds a goroutine and its buffer for as long as
// it likes, and it never has to say another word.
func AnswerHello(s Stream, from node.ID, self func(Badged) Hello, moved func(was, now node.ID)) error {
	c := wire.NewConn(s)

	_ = s.SetReadDeadline(time.Now().Add(settleIn))

	// Reading the ask is also what keeps the two sides in step on one stream.
	_, body, err := c.ReadFrame()
	if err != nil {
		return fmt.Errorf("reading the ask: %w", err)
	}
	_ = s.SetReadDeadline(time.Time{})

	who, was, movedOn := showing(from, body)
	if movedOn && moved != nil {
		moved(was, from)
	}
	return c.WriteFrame(wire.KindOpen, self(who).encode())
}

// ReadHello reads what the far end calls itself.
func ReadHello(s io.ReadWriteCloser) (Hello, error) {
	_, body, err := wire.NewConn(s).ReadFrame()
	if err != nil {
		return Hello{}, fmt.Errorf("reading hello: %w", err)
	}
	return decodeHello(body)
}

// AskHello asks the far end what it is and reads the answer.
//
// The ask is not empty ceremony. A QUIC stream does not reach the other side until something is
// sent on it, so a client that opened one and only read would wait for an answer to a stream the
// server had not yet been handed. This is the byte that makes the stream exist over there.
func AskHello(s io.ReadWriteCloser) (Hello, error) {
	if err := wire.NewConn(s).WriteFrame(wire.KindPing, showable()); err != nil {
		return Hello{}, fmt.Errorf("asking: %w", err)
	}
	return ReadHello(s)
}

// MaxPathShown bounds a path as it is printed. A path is already bounded on the wire; this is what
// keeps one long enough to be legal from pushing the rest of a listing off the screen.
const MaxPathShown = 256

// MaxHolder bounds one name in the list of who else holds a namespace. A user key written the way
// authorized_keys writes one is a few hundred bytes; the general string limit is sixty-four
// kilobytes of somebody else's choosing, times however many they claim.
const MaxHolder = 1024
