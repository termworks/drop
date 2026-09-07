package share

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/wire"
)

func TestReplayBodyRestartsAfterAPartialRead(t *testing.T) {
	replay, err := newReplay(strings.NewReader("whole"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replay.close() })

	prefix := make([]byte, 2)
	if _, err := io.ReadFull(replay.reader(), prefix); err != nil {
		t.Fatal(err)
	}
	if string(prefix) != "wh" || replay.cached != 2 {
		t.Fatalf("prefix = %q, cached = %d", prefix, replay.cached)
	}

	whole, err := io.ReadAll(replay.reader())
	if err != nil {
		t.Fatal(err)
	}
	if string(whole) != "whole" || replay.cached != 5 || !replay.eof {
		t.Fatalf("replay = %q, cached = %d, eof = %v", whole, replay.cached, replay.eof)
	}

	again, err := io.ReadAll(replay.reader())
	if err != nil || string(again) != "whole" {
		t.Fatalf("second replay = %q, %v", again, err)
	}
}

func TestReaderTransferResumesAfterAnEndWriteFails(t *testing.T) {
	transfer, err := NewTransfer([]Source{FileFromReader("report", strings.NewReader("whole"))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transfer.Close() })

	firstReply := transferResponse(t, resume{At: []int64{0}, Done: []bool{false}}, false)
	failed := &failAfterWrites{left: 4}
	err = transfer.Send(wire.NewConn(readWriter{firstReply, failed}), nil)
	if !Unconfirmed(err) {
		t.Fatalf("failed end write returned %v", err)
	}
	if transfer.replays[0].cached != 5 || !transfer.replays[0].eof {
		t.Fatalf("cached = %d, eof = %v", transfer.replays[0].cached, transfer.replays[0].eof)
	}

	retryReply := transferResponse(t, resume{At: []int64{5}, Done: []bool{false}}, true)
	var retried bytes.Buffer
	if err := transfer.Send(wire.NewConn(readWriter{retryReply, &retried}), nil); err != nil {
		t.Fatal(err)
	}
	read := wire.NewConn(readWriter{bytes.NewReader(retried.Bytes()), io.Discard})
	if kind, _, err := read.ReadFrame(); err != nil || kind != wire.KindItem {
		t.Fatalf("offer frame = %d, %v", kind, err)
	}
	kind, body, err := read.ReadFrame()
	if err != nil || kind != wire.KindEnd {
		t.Fatalf("resumed frame = %d, %v", kind, err)
	}
	end, err := wire.DecodeEnd(body)
	if err != nil {
		t.Fatal(err)
	}
	want := blake3.Sum256([]byte("whole"))
	if end.Size != 5 || !bytes.Equal(end.Digest, want[:]) {
		t.Fatalf("resumed end = %+v", end)
	}
}

func TestTransferCloseRemovesReaderReplays(t *testing.T) {
	transfer, err := NewTransfer([]Source{FileFromReader("report", strings.NewReader("whole"))})
	if err != nil {
		t.Fatal(err)
	}
	name := transfer.replays[0].cache.Name()
	stat, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("replay mode = %o", stat.Mode().Perm())
	}
	if err := transfer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closed replay still exists: %v", err)
	}
	if err := transfer.Close(); err != nil {
		t.Fatalf("closing twice: %v", err)
	}
	var sent bytes.Buffer
	if err := transfer.Send(wire.NewConn(readWriter{&bytes.Buffer{}, &sent}), nil); err == nil {
		t.Fatal("closed transfer was sent")
	}
	if sent.Len() != 0 {
		t.Fatalf("closed transfer wrote %d bytes", sent.Len())
	}
}

func TestLocalSourceFailureWinsOverAnEndWriteFailure(t *testing.T) {
	src := Source{Name: "growing", Size: 0, Reader: strings.NewReader("x")}
	err := sendOne(wire.NewConn(readWriter{&bytes.Buffer{}, errorWriter{}}), src, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "changed size") || Unconfirmed(err) {
		t.Fatalf("local source failure returned %v", err)
	}
}

func transferResponse(t *testing.T, picked resume, acknowledge bool) *bytes.Buffer {
	t.Helper()
	var reply bytes.Buffer
	conn := wire.NewConn(readWriter{&reply, &reply})
	if err := conn.WriteFrame(wire.KindAccept, picked.encode()); err != nil {
		t.Fatal(err)
	}
	if acknowledge {
		if err := conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
			t.Fatal(err)
		}
	}
	return &reply
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
