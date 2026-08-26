package wire

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
)

// A duplex stream mixes control frames and bulk data. Reading one must not eat the next.
func TestFramesDoNotBleedIntoEachOther(t *testing.T) {
	var buf bytes.Buffer
	conn := NewConn(readWriter{&buf, &buf})

	payload := bytes.Repeat([]byte{0x5a}, DataChunk)
	if err := conn.WriteFrame(KindOpen, []byte("an open")); err != nil {
		t.Fatalf("WriteFrame(): %v", err)
	}
	if err := conn.WriteData(payload); err != nil {
		t.Fatalf("WriteData(): %v", err)
	}
	if err := conn.WriteFrame(KindEnd, []byte("done")); err != nil {
		t.Fatalf("WriteFrame(): %v", err)
	}

	read := NewConn(readWriter{&buf, io.Discard})

	kind, body, err := read.ReadFrame()
	if err != nil || kind != KindOpen || string(body) != "an open" {
		t.Fatalf("first frame = %d %q, %v", kind, body, err)
	}

	kind, size, err := read.ReadHeader()
	if err != nil || kind != KindData || size != len(payload) {
		t.Fatalf("second frame = kind %d size %d, %v", kind, size, err)
	}
	got := make([]byte, DataChunk)
	if err := read.ReadBody(got, size); err != nil {
		t.Fatalf("ReadBody(): %v", err)
	}
	if !bytes.Equal(got[:size], payload) {
		t.Fatal("the data frame did not survive the round trip")
	}

	kind, body, err = read.ReadFrame()
	if err != nil || kind != KindEnd || string(body) != "done" {
		t.Fatalf("third frame = %d %q, %v", kind, body, err)
	}
}

func TestEmptyFrameIsFine(t *testing.T) {
	var buf bytes.Buffer
	conn := NewConn(readWriter{&buf, &buf})

	if err := conn.WriteFrame(KindPing, nil); err != nil {
		t.Fatalf("WriteFrame(): %v", err)
	}

	kind, body, err := NewConn(readWriter{&buf, io.Discard}).ReadFrame()
	if err != nil || kind != KindPing || len(body) != 0 {
		t.Fatalf("ReadFrame() = %d %q, %v", kind, body, err)
	}
}

// A length past the cap has to be refused before anything is allocated for it.
func TestReadHeaderRefusesAnAbsurdLength(t *testing.T) {
	// Kind, then a varint far above MaxFrame.
	body := []byte{KindData, 0xff, 0xff, 0xff, 0xff, 0x0f}

	_, _, err := NewConn(readWriter{bytes.NewReader(body), io.Discard}).ReadHeader()
	if err == nil {
		t.Fatal("ReadHeader() accepted a frame far past the limit")
	}
}

type readWriter struct {
	io.Reader
	io.Writer
}

// A session ends by the stream ending. That is the one error a reader is allowed to call an
// ordinary finish, and only where a frame was about to start.
func TestClosedTellsAFinishFromAFault(t *testing.T) {
	for _, at := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{io.EOF, true},
		{fmt.Errorf("reading a request: %w", io.EOF), true},
		{net.ErrClosed, true},
		{fmt.Errorf("receiving: %w", net.ErrClosed), true},
		{io.ErrUnexpectedEOF, false},
		{errors.New("something else"), false},
	} {
		if got := Closed(at.err); got != at.want {
			t.Errorf("Closed(%v) = %v, want %v", at.err, got, at.want)
		}
	}
}

// A stream that stops in the middle of a frame header did not finish, whatever the reader under it
// calls that.
func TestAHalfReadHeaderIsNotAFinish(t *testing.T) {
	empty := NewConn(readWriter{bytes.NewReader(nil), io.Discard})
	if _, _, err := empty.ReadHeader(); !Closed(err) {
		t.Errorf("a stream that ended between frames came back as %v", err)
	}

	half := NewConn(readWriter{bytes.NewReader([]byte{KindData}), io.Discard})
	if _, _, err := half.ReadHeader(); Closed(err) {
		t.Errorf("a stream that ended inside a frame came back as a finish: %v", err)
	}
}

// A frame nobody can read is not worth sending.
//
// Every reader refuses a body over the limit at the header, which ends the session — so a writer
// that puts one on the wire has told whoever wrote it nothing about which frame was too big, and
// has broken a connection that was working.
func TestAFrameOverTheLimitIsNotWritten(t *testing.T) {
	var out bytes.Buffer
	c := NewConn(&both{r: bytes.NewReader(nil), w: &out})

	if err := c.WriteFrame(KindItem, make([]byte, MaxFrame+1)); err == nil {
		t.Fatal("a frame over the limit was written")
	}
	if out.Len() != 0 {
		t.Fatalf("%d bytes went out for a frame that was refused", out.Len())
	}

	if err := c.WriteFrame(KindItem, make([]byte, MaxFrame)); err != nil {
		t.Fatalf("a frame of exactly the limit was refused: %v", err)
	}
}

// both is a stream that reads from one place and writes to another.
type both struct {
	r io.Reader
	w io.Writer
}

func (b *both) Read(p []byte) (int, error)  { return b.r.Read(p) }
func (b *both) Write(p []byte) (int, error) { return b.w.Write(p) }
