package proto

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/wire"
)

// lingerFor bounds the wait for the far end to close after both sides have stopped writing.
const lingerFor = 5 * time.Second

// Stream is what a session runs over: a bidirectional byte stream whose write side can be
// closed on its own, which is what a half-close needs.
type Stream interface {
	io.ReadWriteCloser
	SetReadDeadline(t time.Time) error
}

// Duplex is a live stream between two nodes: both ends write whenever they have something, and
// neither knows how much will pass. Terminal sessions and pipes are this.
//
// The two directions are independent. One end finishing does not end the other, the same way a
// pipe closing its input does not stop it producing output.
type Duplex struct {
	conn   *wire.Conn
	stream Stream
	mu     sync.Mutex // one writer at a time, so frames do not interleave mid-body
	// OnResize, when set, is called when the far end reports a new terminal size.
	OnResize func(cols, rows uint16)
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

	if err := d.conn.WriteFrame(wire.KindEnd, End{Size: SizeUnknown}.encode()); err != nil {
		return err
	}
	if d.stream != nil {
		// On a QUIC stream Close ends the write side and leaves reading open, which is the half-close.
		return d.stream.Close()
	}
	return nil
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
			if errors.Is(err, io.EOF) {
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
			if d.OnResize != nil {
				d.OnResize(resize.Cols, resize.Rows)
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

// OpenDuplex starts a live stream to a peer. Name describes what it is, for the far end to show.
func OpenDuplex(ctx context.Context, s Stream, path, name, from string) (*Duplex, error) {
	conn := wire.NewConn(s)
	open := Open{
		Mode:  ModeDuplex,
		Path:  path,
		From:  from,
		Items: []Item{{Name: name, Size: SizeUnknown}},
	}
	open.Badge, open.Signed = carried()

	if err := conn.WriteFrame(wire.KindOpen, open.encode()); err != nil {
		_ = s.Close()
		return nil, err
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("reading the answer: %w", err)
	}
	switch kind {
	case wire.KindAccept:
		return &Duplex{conn: conn, stream: s}, nil
	case wire.KindReject:
		reject, derr := decodeReject(body)
		_ = s.Close()
		if derr != nil {
			return nil, derr
		}
		return nil, Declined{Reason: reject.Reason}
	default:
		_ = s.Close()
		return nil, fmt.Errorf("expected an answer, got frame kind %d", kind)
	}
}
