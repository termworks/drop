package proto

import (
	"fmt"
	"io"

	"github.com/bresilla/drop/src/pkg/wire"
)

// Hello is what a node answers with when asked what it calls itself. Self-declared, so it names a
// peer but never authenticates one; the endpoint id does that.
type Hello struct {
	Name    string
	Version string
}

func (h Hello) encode() []byte {
	w := wire.NewWriter()
	w.String(h.Name)
	w.String(h.Version)
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
	return out, nil
}

// AnswerHello writes this node's description onto a hello stream.
func AnswerHello(s io.ReadWriteCloser, self Hello) error {
	return wire.NewConn(s).WriteFrame(wire.KindOpen, self.encode())
}

// ReadHello reads what the far end calls itself.
func ReadHello(s io.ReadWriteCloser) (Hello, error) {
	_, body, err := wire.NewConn(s).ReadFrame()
	if err != nil {
		return Hello{}, fmt.Errorf("reading hello: %w", err)
	}
	return decodeHello(body)
}
