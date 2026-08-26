package proto

import (
	"fmt"
	"io"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
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
	// Writable says the far end may put something into it.
	Writable bool
	// Locked says this path can be seen but not opened. It is here to be asked for.
	Locked bool
	// About is what this kind of path is for, in the words of whoever wrote it.
	About string
}

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

func (h Hello) encode() []byte {
	w := wire.NewWriter()
	w.String(h.Name)
	w.String(h.Version)

	w.Uint(uint64(len(h.Serves)))
	for _, s := range h.Serves {
		w.String(s.Path)
		w.String(s.Archetype)
		w.Uint(uint64(s.Version))
		w.Bool(s.Writable)
		w.Bool(s.Locked)
		w.String(s.About)
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
	out.Name, out.Version = name, version

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
		out.Serves = append(out.Serves, Served{
			Path:      path,
			Archetype: archetype,
			Version:   int(version),
			Writable:  writable,
			Locked:    locked,
			About:     about,
		})
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
			said.Writable, said.About = open && note.Writable, note.About
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

// AnswerHello reads the ask and writes this node's description back.
// AnswerHello reads the ask and answers it. Self is built from the badge the ask carried, because
// what this node is willing to say it serves depends on who is asking.
func AnswerHello(s io.ReadWriteCloser, from node.ID, self func(Badged) Hello) error {
	c := wire.NewConn(s)

	// Reading the ask is also what keeps the two sides in step on one stream.
	_, body, err := c.ReadFrame()
	if err != nil {
		return fmt.Errorf("reading the ask: %w", err)
	}
	return c.WriteFrame(wire.KindOpen, self(showing(from, body)).encode())
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
