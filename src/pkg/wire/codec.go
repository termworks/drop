// Package wire is drop's binary encoding: varint-based, no reflection, no JSON.
package wire

import (
	"encoding/binary"
	"fmt"
	"math"
)

// MaxString caps any single decoded string, so a length read off the wire cannot ask for an
// arbitrary allocation.
const MaxString = 1 << 16

// Writer builds a message body.
type Writer struct {
	buf []byte
	num [binary.MaxVarintLen64]byte
}

func NewWriter() *Writer {
	return &Writer{buf: make([]byte, 0, 64)}
}

func (w *Writer) Byte(v byte) {
	w.buf = append(w.buf, v)
}

func (w *Writer) Bool(v bool) {
	if v {
		w.buf = append(w.buf, 1)
		return
	}
	w.buf = append(w.buf, 0)
}

// Uint writes an unsigned varint: one byte for values under 128, which is most of them.
func (w *Writer) Uint(v uint64) {
	n := binary.PutUvarint(w.num[:], v)
	w.buf = append(w.buf, w.num[:n]...)
}

// Int writes a zigzag varint, so small negatives cost one byte too. Sizes use -1 for "unknown".
func (w *Writer) Int(v int64) {
	n := binary.PutVarint(w.num[:], v)
	w.buf = append(w.buf, w.num[:n]...)
}

func (w *Writer) Bytes(v []byte) {
	w.Uint(uint64(len(v)))
	w.buf = append(w.buf, v...)
}

func (w *Writer) String(v string) {
	w.Uint(uint64(len(v)))
	w.buf = append(w.buf, v...)
}

// Body is what was built. Valid until the next write.
func (w *Writer) Body() []byte {
	return w.buf
}

// Reader takes a message body apart. Every method checks its bounds, so a malformed or hostile
// message returns an error rather than panicking or over-allocating.
type Reader struct {
	buf []byte
	at  int
}

func NewReader(body []byte) *Reader {
	return &Reader{buf: body}
}

func (r *Reader) Byte() (byte, error) {
	if r.at >= len(r.buf) {
		return 0, fmt.Errorf("wire: message ended early")
	}
	v := r.buf[r.at]
	r.at++
	return v, nil
}

func (r *Reader) Bool() (bool, error) {
	v, err := r.Byte()
	return v == 1, err
}

func (r *Reader) Uint() (uint64, error) {
	v, n := binary.Uvarint(r.buf[r.at:])
	if n <= 0 {
		return 0, fmt.Errorf("wire: malformed unsigned varint")
	}
	r.at += n
	return v, nil
}

func (r *Reader) Int() (int64, error) {
	v, n := binary.Varint(r.buf[r.at:])
	if n <= 0 {
		return 0, fmt.Errorf("wire: malformed signed varint")
	}
	r.at += n
	return v, nil
}

func (r *Reader) Bytes(max int) ([]byte, error) {
	size, err := r.Uint()
	if err != nil {
		return nil, err
	}
	if size > uint64(max) || size > math.MaxInt32 {
		return nil, fmt.Errorf("wire: field claims %d bytes, over the %d limit", size, max)
	}
	if r.at+int(size) > len(r.buf) {
		return nil, fmt.Errorf("wire: field claims %d bytes but only %d remain", size, len(r.buf)-r.at)
	}
	out := r.buf[r.at : r.at+int(size)]
	r.at += int(size)
	return out, nil
}

func (r *Reader) String(max int) (string, error) {
	raw, err := r.Bytes(max)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Done reports whether every byte was consumed.
func (r *Reader) Done() bool {
	return r.at >= len(r.buf)
}
