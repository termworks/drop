package live

import (
	"bytes"
	"io"
	"net"
	"testing"

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
