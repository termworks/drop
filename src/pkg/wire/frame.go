package wire

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// Frame kinds. Control frames carry a codec body; DATA carries raw bytes.
const (
	KindOpen   byte = 1
	KindAccept byte = 2
	KindReject byte = 3
	KindItem   byte = 4
	KindData   byte = 5
	KindEnd    byte = 6
	KindAck    byte = 7
	KindResize byte = 8
	KindPing   byte = 9
	KindPong   byte = 10
	// KindRequest and KindReply are one round of a browse session: an operation on a files
	// namespace, and what came of it.
	KindRequest byte = 11
	KindReply   byte = 12
)

// MaxFrame caps a single frame. Data is chunked well under this; a control frame never approaches
// it. A length larger than this is refused before anything is allocated.
const MaxFrame = 1 << 22

// DataChunk is how much payload one data frame carries. The frame header costs a handful of bytes
// against this, so the overhead is not worth measuring.
const DataChunk = 256 << 10

// Conn frames a bidirectional byte stream.
//
// Every read goes through one buffered reader. That is what makes it safe to mix framed control
// messages with bulk data on the same stream: nothing else reads from the underlying stream, so
// nothing can consume bytes that belong to the next frame.
type Conn struct {
	r   *bufio.Reader
	w   io.Writer
	hdr [1 + binary.MaxVarintLen64]byte
}

func NewConn(rw io.ReadWriter) *Conn {
	return &Conn{r: bufio.NewReaderSize(rw, 64<<10), w: rw}
}

// WriteFrame writes one frame: a kind, a length, and the body.
func (c *Conn) WriteFrame(kind byte, body []byte) error {
	if err := c.writeHeader(kind, len(body)); err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	if _, err := c.w.Write(body); err != nil {
		return fmt.Errorf("wire: writing a frame body: %w", err)
	}
	return nil
}

func (c *Conn) writeHeader(kind byte, size int) error {
	c.hdr[0] = kind
	n := binary.PutUvarint(c.hdr[1:], uint64(size))
	if _, err := c.w.Write(c.hdr[:1+n]); err != nil {
		return fmt.Errorf("wire: writing a frame header: %w", err)
	}
	return nil
}

// WriteData writes one data frame without copying the payload.
func (c *Conn) WriteData(payload []byte) error {
	return c.WriteFrame(KindData, payload)
}

// ReadHeader reads the next frame's kind and body length, leaving the body on the stream.
func (c *Conn) ReadHeader() (kind byte, size int, err error) {
	kind, err = c.r.ReadByte()
	if err != nil {
		return 0, 0, err
	}

	length, err := binary.ReadUvarint(c.r)
	if err != nil {
		return 0, 0, fmt.Errorf("wire: reading a frame length: %w", err)
	}
	if length > MaxFrame {
		return 0, 0, fmt.Errorf("wire: frame claims %d bytes, over the %d limit", length, MaxFrame)
	}
	return kind, int(length), nil
}

// ReadBody fills buf with exactly size bytes of frame body.
func (c *Conn) ReadBody(buf []byte, size int) error {
	if size > len(buf) {
		return fmt.Errorf("wire: frame body is %d bytes, buffer holds %d", size, len(buf))
	}
	if _, err := io.ReadFull(c.r, buf[:size]); err != nil {
		return fmt.Errorf("wire: reading a frame body: %w", err)
	}
	return nil
}

// ReadFrame reads a whole frame, allocating for the body. For bulk data prefer ReadHeader and
// ReadBody with a reused buffer.
func (c *Conn) ReadFrame() (kind byte, body []byte, err error) {
	kind, size, err := c.ReadHeader()
	if err != nil {
		return 0, nil, err
	}
	if size == 0 {
		return kind, nil, nil
	}
	body = make([]byte, size)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return 0, nil, fmt.Errorf("wire: reading a frame body: %w", err)
	}
	return kind, body, nil
}

// Discard throws away a frame body that is not wanted.
func (c *Conn) Discard(size int) error {
	if _, err := c.r.Discard(size); err != nil {
		return fmt.Errorf("wire: skipping a frame body: %w", err)
	}
	return nil
}
