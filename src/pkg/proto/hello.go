package proto

import (
	"fmt"
	"io"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// MaxServed bounds how many namespaces a node will describe or believe. A list is a convenience,
// not a thing worth allocating without limit for.
const MaxServed = 256

// Served is one namespace a node offers.
type Served struct {
	Path      string
	Archetype ns.Archetype
	// Writable says the far end may put something into it: a share that accepts, a files namespace
	// that allows writes, or a tty that takes input.
	Writable bool
	// Locked says this path can be seen but not opened. It is here to be asked for.
	Locked bool
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
		w.Byte(byte(s.Archetype))
		w.Bool(s.Writable)
		w.Bool(s.Locked)
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
		archetype, err := r.Byte()
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
		out.Serves = append(out.Serves, Served{
			Path:      path,
			Archetype: ns.Archetype(archetype),
			Writable:  writable,
			Locked:    locked,
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
func Describe(table *ns.Table, caller ns.Caller) []Served {
	if table == nil {
		return nil
	}

	var out []Served
	for _, m := range table.All() {
		open, _ := table.Admits(m.Path, caller)
		if !open && !table.Sees(m.Path, caller) {
			continue
		}
		out = append(out, Served{
			Path:      m.Path,
			Archetype: m.Archetype,
			Writable:  open && writable(m),
			Locked:    !open,
		})
	}
	return out
}

// writable reports whether the far end may send into a namespace.
func writable(m ns.Mount) bool {
	switch m.Archetype {
	case ns.Share, ns.Chat, ns.Link:
		return true
	case ns.Files:
		return m.Writable
	case ns.TTY:
		return m.Input
	default:
		return false
	}
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
