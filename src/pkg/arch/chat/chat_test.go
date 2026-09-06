package chat

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

type readWriter struct {
	io.Reader
	io.Writer
}

func messageFrames(t *testing.T, messages []convo.Message, count int64) *bytes.Buffer {
	t.Helper()
	var in bytes.Buffer
	conn := wire.NewConn(readWriter{&in, &in})
	for _, message := range messages {
		if err := conn.WriteFrame(wire.KindItem, message.Encode()); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.WriteFrame(wire.KindEnd, wire.End{Size: count}.Encode()); err != nil {
		t.Fatal(err)
	}
	return &in
}

func TestTakeAcknowledgesOnlyStoredMessages(t *testing.T) {
	messages := []convo.Message{{ID: "one"}, {ID: "two"}}
	in, out := messageFrames(t, messages, 2), &bytes.Buffer{}

	err := Take(wire.NewConn(readWriter{in, out}), node.ID{}, func(_ node.ID, message convo.Message) error {
		if message.ID == "two" {
			return fmt.Errorf("disk unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Take(): %v", err)
	}

	conn := wire.NewConn(readWriter{out, &bytes.Buffer{}})
	kind, body, err := conn.ReadFrame()
	if err != nil || kind != wire.KindAck {
		t.Fatalf("reading the acknowledgement: kind=%d err=%v", kind, err)
	}
	ids, err := decodeStored(body)
	if err != nil || len(ids) != 1 || ids[0] != "one" {
		t.Fatalf("acknowledged %v (%v)", ids, err)
	}
}

func TestTakeRefusesAnIncorrectMessageCount(t *testing.T) {
	in := messageFrames(t, []convo.Message{{ID: "one"}}, 2)
	err := Take(wire.NewConn(readWriter{in, &bytes.Buffer{}}), node.ID{}, func(node.ID, convo.Message) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "after sending 1") {
		t.Fatalf("the incorrect count was answered with %v", err)
	}
}

func TestSendRefusesAnOversizedBatchBeforeWriting(t *testing.T) {
	batch := make([]convo.Message, MaxBatch+1)
	var stream bytes.Buffer
	_, err := Send(wire.NewConn(readWriter{&stream, &stream}), batch)
	if err == nil || stream.Len() != 0 {
		t.Fatalf("Send() returned %v after writing %d bytes", err, stream.Len())
	}
}

func TestReceiptRefusesTrailingBytes(t *testing.T) {
	body := append(encodeStored([]string{"one"}), 0)
	if _, err := decodeStored(body); err == nil {
		t.Fatal("a receipt with trailing bytes was accepted")
	}
}
