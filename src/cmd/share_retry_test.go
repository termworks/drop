package cmd

import (
	stdbytes "bytes"
	"context"
	"io"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch/share"
	"github.com/bresilla/drop/src/pkg/wire"
)

type transferReadWriter struct {
	io.Reader
	io.Writer
}

func transferReplies(t *testing.T, at int64, done, acknowledge bool) *stdbytes.Buffer {
	t.Helper()
	var replies stdbytes.Buffer
	conn := wire.NewConn(transferReadWriter{Reader: &replies, Writer: &replies})
	w := wire.NewWriter()
	w.Uint(1)
	w.Int(at)
	w.Bool(done)
	if err := conn.WriteFrame(wire.KindAccept, w.Body()); err != nil {
		t.Fatal(err)
	}
	if acknowledge {
		if err := conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
			t.Fatal(err)
		}
	}
	return &replies
}

func TestAnUnconfirmedTransferIsRetried(t *testing.T) {
	transfer, err := share.NewTransfer([]share.Source{share.FileFromReader("report", stdbytes.NewReader([]byte("whole")))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transfer.Close() })
	attempts, closed := 0, 0
	open := func(context.Context) (*wire.Conn, func(), error) {
		attempts++
		replies := transferReplies(t, 0, false, false)
		if attempts == 2 {
			replies = transferReplies(t, 5, true, true)
		}
		return wire.NewConn(transferReadWriter{Reader: replies, Writer: &stdbytes.Buffer{}}), func() { closed++ }, nil
	}

	if err := retryTransfer(t.Context(), transfer, nil, open); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || closed != 2 {
		t.Fatalf("made %d attempts and closed %d streams", attempts, closed)
	}
}

func TestASettledTransferFailureIsNotRetried(t *testing.T) {
	transfer, err := share.NewTransfer(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transfer.Close() })
	attempts := 0
	open := func(context.Context) (*wire.Conn, func(), error) {
		attempts++
		var replies stdbytes.Buffer
		conn := wire.NewConn(transferReadWriter{Reader: &replies, Writer: &replies})
		if err := conn.WriteFrame(wire.KindReject, wire.Reject{Reason: "closed"}.Encode()); err != nil {
			t.Fatal(err)
		}
		return wire.NewConn(transferReadWriter{Reader: &replies, Writer: &stdbytes.Buffer{}}), func() {}, nil
	}

	if err := retryTransfer(t.Context(), transfer, nil, open); err == nil {
		t.Fatal("rejected transfer returned no error")
	}
	if attempts != 1 {
		t.Fatalf("rejected transfer made %d attempts", attempts)
	}
}

func TestCancellationStopsAnUnconfirmedRetry(t *testing.T) {
	transfer, err := share.NewTransfer([]share.Source{share.FileFromReader("report", stdbytes.NewReader([]byte("whole")))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transfer.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	open := func(context.Context) (*wire.Conn, func(), error) {
		attempts++
		replies := transferReplies(t, 0, false, false)
		return wire.NewConn(transferReadWriter{Reader: replies, Writer: &stdbytes.Buffer{}}), cancel, nil
	}

	if err := retryTransfer(ctx, transfer, nil, open); err == nil {
		t.Fatal("cancelled transfer returned no error")
	}
	if attempts != 1 {
		t.Fatalf("cancelled transfer made %d attempts", attempts)
	}
}

func TestUnconfirmedTransferRetriesAreBounded(t *testing.T) {
	transfer, err := share.NewTransfer([]share.Source{share.FileFromReader("report", stdbytes.NewReader([]byte("whole")))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transfer.Close() })
	attempts, closed := 0, 0
	open := func(context.Context) (*wire.Conn, func(), error) {
		attempts++
		replies := transferReplies(t, 0, false, false)
		if attempts > 1 {
			replies = transferReplies(t, 5, true, false)
		}
		return wire.NewConn(transferReadWriter{Reader: replies, Writer: &stdbytes.Buffer{}}), func() { closed++ }, nil
	}

	err = retryTransfer(t.Context(), transfer, nil, open)
	if !share.Unconfirmed(err) {
		t.Fatalf("bounded retries returned %v", err)
	}
	if attempts != shareAttempts || closed != shareAttempts {
		t.Fatalf("made %d attempts and closed %d streams", attempts, closed)
	}
}
