package live

import (
	"bytes"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/wire"
)

type readWriter struct {
	io.Reader
	io.Writer
}

// io.Copy stops on a short write, so Write has to report every byte it took.
func TestDuplexWriteReportsWhatItTook(t *testing.T) {
	var buf bytes.Buffer
	d := &Duplex{conn: wire.NewConn(readWriter{&buf, &buf})}

	payload := bytes.Repeat([]byte{0x21}, 3*wire.DataChunk+17)
	n, err := d.Write(payload)
	if err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if n != len(payload) {
		t.Fatalf("Write() = %d, want %d — io.Copy would call this a short write", n, len(payload))
	}
}

// The whole point of a duplex stream: bytes go through without anyone knowing the total.
func TestDuplexCarriesAnUnboundedStream(t *testing.T) {
	var wire1 bytes.Buffer

	sender := &Duplex{conn: wireConn(&wire1)}
	payload := bytes.Repeat([]byte("live"), 100_000)
	if _, err := io.Copy(sender, bytes.NewReader(payload)); err != nil {
		t.Fatalf("io.Copy() into the stream: %v", err)
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	var got bytes.Buffer
	receiver := &Duplex{conn: wireConn(&wire1)}
	if err := receiver.Pump(&got); err != nil {
		t.Fatalf("Pump(): %v", err)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("stream did not survive: got %d bytes, sent %d", got.Len(), len(payload))
	}
}

func TestDuplexReportsResize(t *testing.T) {
	var buf bytes.Buffer

	sender := &Duplex{conn: wireConn(&buf)}
	if err := sender.Resize(120, 40); err != nil {
		t.Fatalf("Resize(): %v", err)
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	var cols, rows uint16
	receiver := &Duplex{conn: wireConn(&buf), OnResize: func(c, r uint16) { cols, rows = c, r }}
	if err := receiver.Pump(io.Discard); err != nil {
		t.Fatalf("Pump(): %v", err)
	}
	if cols != 120 || rows != 40 {
		t.Fatalf("resize came through as %dx%d, want 120x40", cols, rows)
	}
}

func wireConn(buf *bytes.Buffer) *wire.Conn {
	return wire.NewConn(readWriter{buf, buf})
}

// shut is a stream whose transport reports that the peer closed the connection, which is what a
// QUIC stream does when the far end goes away in the ordinary way.
type shut struct{}

func (shut) Read([]byte) (int, error) { return 0, net.ErrClosed }

// A peer that closes is a peer that finished writing. The stream ending and the transport saying so
// are the same news.
func TestPumpTakesAClosedTransportAsTheEnd(t *testing.T) {
	d := &Duplex{conn: wire.NewConn(readWriter{shut{}, io.Discard})}
	if err := d.Pump(io.Discard); err != nil {
		t.Fatalf("Pump() came back as %v", err)
	}
}

// A stream that dies in the middle of a frame did not finish, and is still reported.
func TestPumpReportsAStreamThatDiesMidFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := wireConn(&buf).WriteData(bytes.Repeat([]byte{0x7f}, 64)); err != nil {
		t.Fatalf("WriteData(): %v", err)
	}
	cut := buf.Bytes()[:8]

	d := &Duplex{conn: wire.NewConn(readWriter{bytes.NewReader(cut), io.Discard})}
	if err := d.Pump(io.Discard); err == nil {
		t.Fatal("Pump() called a half-arrived frame the end of the stream")
	}
}

// A terminal's shape is somebody else's number, and a grid is kept cell by cell at both ends of
// this. A screen nobody could be looking at is not passed on as one.
func TestAnEnormousResizeIsHeldToAScreen(t *testing.T) {
	cols, rows, told := resized(t, 65535, 65535)

	if !told {
		t.Fatal("a resize was dropped entirely")
	}
	if cols > mostSide || rows > mostSide {
		t.Fatalf("a 65535x65535 terminal came through as %dx%d", cols, rows)
	}
}

// A shape with nothing in it is not a shape. It reaches a pty as a window every program on it is
// told to draw for, and a screen as a grid with no cells.
func TestAnEmptyResizeIsNotPassedOn(t *testing.T) {
	if _, _, told := resized(t, 0, 0); told {
		t.Fatal("a 0x0 terminal was passed on as a size")
	}
}

// resized sends one shape and reports what the far end was told.
func resized(t *testing.T, cols, rows uint16) (uint16, uint16, bool) {
	t.Helper()

	var buf bytes.Buffer
	sender := &Duplex{conn: wireConn(&buf)}
	if err := sender.Resize(cols, rows); err != nil {
		t.Fatalf("Resize(): %v", err)
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	var gotCols, gotRows uint16
	told := false
	receiver := &Duplex{conn: wireConn(&buf), OnResize: func(c, r uint16) {
		gotCols, gotRows, told = c, r, true
	}}
	if err := receiver.Pump(io.Discard); err != nil {
		t.Fatalf("Pump(): %v", err)
	}
	return gotCols, gotRows, told
}

// A pump waiting on a far end that has nothing more to say holds the goroutine it is on, and the
// session it belongs to has already ended.
func TestStopEndsAPumpWaitingOnTheFarEnd(t *testing.T) {
	s := newQuiet()
	d := New(wire.NewConn(s), s)

	done := make(chan error, 1)
	go func() { done <- d.Pump(io.Discard) }()

	d.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Pump stayed on a stream whose read side had been ended")
	}
}

// quiet is a stream nobody is saying anything on: writes land, reads wait, and a read deadline in
// the past is what ends a read already in flight.
type quiet struct {
	mu   sync.Mutex
	past bool
	wake chan struct{}
	sent bytes.Buffer
}

func newQuiet() *quiet { return &quiet{wake: make(chan struct{})} }

func (q *quiet) Read(p []byte) (int, error) {
	q.mu.Lock()
	wake, past := q.wake, q.past
	q.mu.Unlock()

	if !past {
		<-wake
	}
	return 0, os.ErrDeadlineExceeded
}

func (q *quiet) Write(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.sent.Write(p)
}

func (q *quiet) Close() error { return nil }

func (q *quiet) SetReadDeadline(t time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !t.IsZero() && !t.After(time.Now()) && !q.past {
		q.past = true
		close(q.wake)
	}
	return nil
}
