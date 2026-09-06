package wire

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
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

func TestEmptyDataIsNotWritten(t *testing.T) {
	var out bytes.Buffer
	conn := NewConn(readWriter{bytes.NewReader(nil), &out})

	if err := conn.WriteData(nil); err == nil {
		t.Fatal("WriteData() accepted an empty payload")
	}
	if out.Len() != 0 {
		t.Fatalf("WriteData() wrote %d bytes for an empty payload", out.Len())
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

func TestAFrameSurvivesShortWrites(t *testing.T) {
	var raw bytes.Buffer
	out := &shortWriter{into: &raw, most: 1}
	conn := NewConn(readWriter{bytes.NewReader(nil), out})
	body := []byte("the entire body")
	if err := conn.WriteFrame(KindItem, body); err != nil {
		t.Fatalf("WriteFrame(): %v", err)
	}

	kind, got, err := NewConn(readWriter{&raw, io.Discard}).ReadFrame()
	if err != nil || kind != KindItem || !bytes.Equal(got, body) {
		t.Fatalf("ReadFrame() = %d %q, %v", kind, got, err)
	}
}

func TestAWriterThatMakesNoProgressIsRefused(t *testing.T) {
	conn := NewConn(readWriter{bytes.NewReader(nil), shortWriter{}})
	if err := conn.WriteFrame(KindItem, []byte("body")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteFrame() = %v, want io.ErrShortWrite", err)
	}
}

func TestNegativeBodySizesAreRefused(t *testing.T) {
	conn := NewConn(readWriter{bytes.NewReader(nil), io.Discard})
	if err := conn.ReadBody(nil, -1); err == nil {
		t.Fatal("ReadBody() accepted a negative size")
	}
	if err := conn.Discard(-1); err == nil {
		t.Fatal("Discard() accepted a negative size")
	}
}

func TestReadFrameUpToHonoursItsLocalLimit(t *testing.T) {
	var stream bytes.Buffer
	written := NewConn(readWriter{&stream, &stream})
	if err := written.WriteFrame(KindOpen, []byte("opening")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewConn(readWriter{&stream, io.Discard}).ReadFrameUpTo(3); err == nil {
		t.Fatal("ReadFrameUpTo() accepted a body over its local limit")
	}
}

func TestReadFrameUpToReadsACompleteFrame(t *testing.T) {
	var stream bytes.Buffer
	written := NewConn(readWriter{&stream, &stream})
	if err := written.WriteFrame(KindOpen, []byte("opening")); err != nil {
		t.Fatal(err)
	}
	kind, body, err := NewConn(readWriter{&stream, io.Discard}).ReadFrameUpTo(16)
	if err != nil || kind != KindOpen || string(body) != "opening" {
		t.Fatalf("ReadFrameUpTo() = %d %q, %v", kind, body, err)
	}
}

func TestReadFrameUpToReportsATruncatedBody(t *testing.T) {
	stream := []byte{KindOpen, 4, 'a'}
	if _, _, err := NewConn(readWriter{bytes.NewReader(stream), io.Discard}).ReadFrameUpTo(16); err == nil {
		t.Fatal("ReadFrameUpTo() accepted a truncated body")
	}
}

func TestReadIdleStopsAStalledFrame(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	conn := NewConn(client)
	started := time.Now()
	err := conn.WithReadIdle(20*time.Millisecond, func() error {
		_, _, err := conn.ReadFrame()
		return err
	})
	if err == nil {
		t.Fatal("a stalled frame outlived its read-idle limit")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("a stalled frame took %s to stop", time.Since(started))
	}
}

func TestReadIdleIsClearedAfterTheOperation(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	conn := NewConn(client)
	if err := conn.WithReadIdle(20*time.Millisecond, func() error { return nil }); err != nil {
		t.Fatal(err)
	}

	written := make(chan error, 1)
	go func() { written <- NewConn(server).WriteFrame(KindPing, nil) }()
	time.Sleep(40 * time.Millisecond)
	kind, _, err := conn.ReadFrame()
	if err != nil || kind != KindPing {
		t.Fatalf("unguarded read = kind %d, %v", kind, err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}

func TestReadIdleRefreshesEveryFramePart(t *testing.T) {
	var framed bytes.Buffer
	if err := NewConn(readWriter{&framed, &framed}).WriteFrame(KindItem, []byte("body")); err != nil {
		t.Fatal(err)
	}
	stream := &deadlineStream{Reader: &framed, Writer: io.Discard}
	conn := NewConn(stream)
	if err := conn.WithReadIdle(time.Second, func() error {
		_, _, err := conn.ReadFrame()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(stream.readSet) != 4 {
		t.Fatalf("read deadlines were set %d times, want initial, header, body and clear", len(stream.readSet))
	}
	for i, at := range stream.readSet[:len(stream.readSet)-1] {
		if at.IsZero() {
			t.Fatalf("read deadline %d was cleared early", i)
		}
	}
	if !stream.readSet[len(stream.readSet)-1].IsZero() {
		t.Fatal("the final read deadline was not cleared")
	}
}

func TestIdleStopsAStalledWriteAndClearsTheDeadline(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	conn := NewConn(client)
	started := time.Now()
	err := conn.WithIdle(20*time.Millisecond, func() error {
		return conn.WriteFrame(KindPing, nil)
	})
	if err == nil {
		t.Fatal("a stalled write outlived its idle limit")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("a stalled write took %s to stop", time.Since(started))
	}

	read := make(chan error, 1)
	go func() {
		kind, _, err := NewConn(server).ReadFrame()
		if err == nil && kind != KindPing {
			err = fmt.Errorf("frame kind %d, want %d", kind, KindPing)
		}
		read <- err
	}()
	time.Sleep(40 * time.Millisecond)
	if err := conn.WriteFrame(KindPing, nil); err != nil {
		t.Fatalf("write after the guard = %v", err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
}

func TestIdleRefreshesEveryWrittenFramePart(t *testing.T) {
	var framed bytes.Buffer
	stream := &deadlineStream{Reader: &bytes.Buffer{}, Writer: &framed}
	conn := NewConn(stream)
	if err := conn.WithIdle(time.Second, func() error {
		return conn.WriteFrame(KindItem, []byte("body"))
	}); err != nil {
		t.Fatal(err)
	}
	if len(stream.readSet) != 2 {
		t.Fatalf("read deadlines were set %d times, want initial and clear", len(stream.readSet))
	}
	if len(stream.writeSet) != 4 {
		t.Fatalf("write deadlines were set %d times, want initial, header, body and clear", len(stream.writeSet))
	}
	if !stream.readSet[len(stream.readSet)-1].IsZero() || !stream.writeSet[len(stream.writeSet)-1].IsZero() {
		t.Fatal("the final idle deadlines were not cleared")
	}
}

func TestIdleResetIgnoresAClosedStream(t *testing.T) {
	stream := &deadlineStream{
		Reader:        &bytes.Buffer{},
		Writer:        io.Discard,
		readResetErr:  io.ErrClosedPipe,
		writeResetErr: net.ErrClosed,
	}
	if err := NewConn(stream).WithIdle(time.Second, func() error { return nil }); err != nil {
		t.Fatalf("closed stream reset = %v", err)
	}
}

func TestIdleResetReportsAnOpenStreamFailure(t *testing.T) {
	want := errors.New("reset failed")
	stream := &deadlineStream{Reader: &bytes.Buffer{}, Writer: io.Discard, writeResetErr: want}
	err := NewConn(stream).WithIdle(time.Second, func() error { return nil })
	if !errors.Is(err, want) {
		t.Fatalf("deadline reset = %v, want %v", err, want)
	}
}

// both is a stream that reads from one place and writes to another.
type both struct {
	r io.Reader
	w io.Writer
}

type deadlineStream struct {
	io.Reader
	io.Writer
	readSet       []time.Time
	writeSet      []time.Time
	readResetErr  error
	writeResetErr error
}

func (s *deadlineStream) SetReadDeadline(at time.Time) error {
	s.readSet = append(s.readSet, at)
	if at.IsZero() {
		return s.readResetErr
	}
	return nil
}

func (s *deadlineStream) SetWriteDeadline(at time.Time) error {
	s.writeSet = append(s.writeSet, at)
	if at.IsZero() {
		return s.writeResetErr
	}
	return nil
}

func (b *both) Read(p []byte) (int, error)  { return b.r.Read(p) }
func (b *both) Write(p []byte) (int, error) { return b.w.Write(p) }

type shortWriter struct {
	into *bytes.Buffer
	most int
}

func (w shortWriter) Write(p []byte) (int, error) {
	if w.most == 0 {
		return 0, nil
	}
	return w.into.Write(p[:min(len(p), w.most)])
}
