package proto

import (
	"fmt"
	"io"

	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// MaxServed bounds how many namespaces a node will describe or believe. A list is a convenience,
// not a thing worth allocating without limit for.
const MaxServed = 256

// Served is one namespace a node offers.
type Served struct {
	Path string
	Kind ns.Kind
	// Writable says the far end may send into it: a files namespace that accepts, or a tty that
	// takes input. It is what the page needs to know whether to offer a way to send.
	Writable bool
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
		w.Byte(byte(s.Kind))
		w.Bool(s.Writable)
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

	// An older node stops here, and that is a node with nothing to say about what it serves rather
	// than a broken one.
	if r.Done() {
		return out, nil
	}

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
		kind, err := r.Byte()
		if err != nil {
			return out, err
		}
		writable, err := r.Bool()
		if err != nil {
			return out, err
		}
		out.Serves = append(out.Serves, Served{Path: path, Kind: ns.Kind(kind), Writable: writable})
	}
	return out, nil
}

// Describe lists what a table serves, in the order a person would read it.
func Describe(table *ns.Table) []Served {
	if table == nil {
		return nil
	}

	var out []Served
	for _, m := range table.All() {
		out = append(out, Served{Path: m.Path, Kind: m.Kind, Writable: writable(m)})
	}
	return out
}

// writable reports whether the far end may send into a namespace.
func writable(m ns.Mount) bool {
	switch m.Kind {
	case ns.KindFiles, ns.KindChat, ns.KindLink:
		return true
	case ns.KindTTY:
		return m.Input
	default:
		return false
	}
}

// AnswerHello reads the ask and writes this node's description back.
func AnswerHello(s io.ReadWriteCloser, self Hello) error {
	c := wire.NewConn(s)

	// The ask carries nothing; reading it is what keeps the two sides in step on one stream.
	if _, _, err := c.ReadFrame(); err != nil {
		return fmt.Errorf("reading the ask: %w", err)
	}
	return c.WriteFrame(wire.KindOpen, self.encode())
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
	if err := wire.NewConn(s).WriteFrame(wire.KindPing, nil); err != nil {
		return Hello{}, fmt.Errorf("asking: %w", err)
	}
	return ReadHello(s)
}
