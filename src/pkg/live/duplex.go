// Package live is a stream both ends write on.
//
// Neither end knows how much will pass and neither has to stop when the other does, the way a pipe
// closing its input does not stop it producing output. A terminal being watched and a command being
// read are both this, which is why it sits under the archetypes that serve them rather than inside
// one of them.
package live

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/wire"
)

// lingerFor bounds the wait for the far end to close after both sides have stopped writing.
const lingerFor = 5 * time.Second

// Stream is what a session runs over: a bidirectional byte stream whose write side can be closed on
// its own, which is what a half-close needs.
type Stream interface {
	io.ReadWriteCloser
	SetReadDeadline(t time.Time) error
}

// A terminal's shape, as far as one is believed. Zero is not a size anything can be drawn on, and a
// grid is kept cell by cell at both ends of this, so a number past any real screen is an allocation
// somebody else asked for rather than a terminal.
const (
	leastSide = 1
	mostSide  = 1000
)

// Resize reports a new terminal size.
type Resize struct {
	Cols uint16
	Rows uint16
}

// shape is the reported size as it is passed on. ok is false when there is no size in it at all: a
// namespace with no terminal behind it says zero, and a window that has just been hung up says it
// too.
func (z Resize) shape() (cols, rows uint16, ok bool) {
	if z.Cols < leastSide || z.Rows < leastSide {
		return 0, 0, false
	}
	return min(z.Cols, mostSide), min(z.Rows, mostSide), true
}

func (z Resize) encode() []byte {
	w := wire.NewWriter()
	w.Uint(uint64(z.Cols))
	w.Uint(uint64(z.Rows))
	return w.Body()
}

func decodeResize(body []byte) (Resize, error) {
	var out Resize

	r := wire.NewReader(body)
	cols, err := r.Uint()
	if err != nil {
		return out, err
	}
	rows, err := r.Uint()
	if err != nil {
		return out, err
	}
	out.Cols, out.Rows = uint16(cols), uint16(rows)
	return out, nil
}

// Duplex is a live stream between two nodes: both ends write whenever they have something, and
// neither knows how much will pass.
//
// The two directions are independent. One end finishing does not end the other.
type Duplex struct {
	conn   *wire.Conn
	stream Stream
	mu     sync.Mutex // one writer at a time, so frames do not interleave mid-body
	// OnResize, when set, is called when the far end reports a new terminal size.
	OnResize func(cols, rows uint16)
}

// New takes a stream that has already been accepted and runs a duplex over it.
func New(conn *wire.Conn, s Stream) *Duplex {
	return &Duplex{conn: conn, stream: s}
}

// Write sends bytes, split into data frames.
func (d *Duplex) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// The count is taken before the loop consumes p. Returning the leftover length instead reports
	// a short write, and io.Copy stops after the first chunk.
	total := len(p)
	for len(p) > 0 {
		chunk := p
		if len(chunk) > wire.DataChunk {
			chunk = chunk[:wire.DataChunk]
		}
		if err := d.conn.WriteData(chunk); err != nil {
			return total - len(p), err
		}
		p = p[len(chunk):]
	}
	return total, nil
}

// Resize tells the far end the terminal changed shape.
func (d *Duplex) Resize(cols, rows uint16) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.conn.WriteFrame(wire.KindResize, Resize{Cols: cols, Rows: rows}.encode())
}

// Close signals that this end has nothing more to write, and half-closes the stream so the far end
// reads a real end of file. The read direction stays open.
func (d *Duplex) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.conn.WriteFrame(wire.KindEnd, wire.End{Size: wire.SizeUnknown}.Encode()); err != nil {
		return err
	}
	if d.stream != nil {
		// On a QUIC stream Close ends the write side and leaves reading open, which is the half-close.
		return d.stream.Close()
	}
	return nil
}

// Stop ends the read direction, so a Pump waiting on the far end comes back. What it is waiting for
// is a peer that has a reason to say something, and once this end is finished with the session there
// is none.
func (d *Duplex) Stop() {
	if d.stream == nil {
		return
	}
	_ = d.stream.SetReadDeadline(time.Now())
}

// Linger waits for the far end to close, so the last frames written are not lost to a teardown.
// Bounded, because a peer that never closes must not hold this end open.
func (d *Duplex) Linger() {
	if d.stream == nil {
		return
	}
	_ = d.stream.SetReadDeadline(time.Now().Add(lingerFor))
	for {
		_, size, err := d.conn.ReadHeader()
		if err != nil {
			return
		}
		if err := d.conn.Discard(size); err != nil {
			return
		}
	}
}

// Pump reads frames until the far end stops writing, handing data to out and control frames to
// their handlers. It is the read half of the stream, and is meant to run in its own goroutine.
//
// Returning means the far end finished writing. It does not mean this end has to.
func (d *Duplex) Pump(out io.Writer) error {
	buf := make([]byte, wire.DataChunk)

	for {
		kind, size, err := d.conn.ReadHeader()
		if err != nil {
			if wire.Closed(err) {
				return nil
			}
			return err
		}

		switch kind {
		case wire.KindData:
			if err := d.conn.ReadBody(buf, size); err != nil {
				return err
			}
			if _, err := out.Write(buf[:size]); err != nil {
				return fmt.Errorf("writing what arrived: %w", err)
			}

		case wire.KindResize:
			body := make([]byte, size)
			if err := d.conn.ReadBody(body, size); err != nil {
				return err
			}
			resize, err := decodeResize(body)
			if err != nil {
				return err
			}
			cols, rows, ok := resize.shape()
			if ok && d.OnResize != nil {
				d.OnResize(cols, rows)
			}

		case wire.KindEnd:
			return d.conn.Discard(size)

		case wire.KindPing:
			if err := d.conn.Discard(size); err != nil {
				return err
			}
			d.mu.Lock()
			err := d.conn.WriteFrame(wire.KindPong, nil)
			d.mu.Unlock()
			if err != nil {
				return err
			}

		default:
			if err := d.conn.Discard(size); err != nil {
				return err
			}
		}
	}
}
