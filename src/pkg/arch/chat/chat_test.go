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

func TestMessageBatchBytesAreBounded(t *testing.T) {
	body := strings.Repeat("x", convo.MaxBody)
	count := MaxBatchBytes/convo.MaxBody + 1
	batch := make([]convo.Message, count)
	for i := range batch {
		batch[i] = convo.Message{ID: fmt.Sprintf("message-%d", i), Body: body}
	}

	var stream bytes.Buffer
	if _, err := Send(wire.NewConn(readWriter{&stream, &stream}), batch); err == nil {
		t.Fatal("Send() accepted an oversized message payload")
	}
	if stream.Len() != 0 {
		t.Fatalf("Send() wrote %d bytes before rejecting the payload", stream.Len())
	}

	in := messageFrames(t, batch, int64(len(batch)))
	if err := Take(wire.NewConn(readWriter{in, &bytes.Buffer{}}), node.ID{}, func(node.ID, convo.Message) error { return nil }); err == nil {
		t.Fatal("Take() accepted an oversized message payload")
	}
}

func TestReceiptRefusesTrailingBytes(t *testing.T) {
	body := append(encodeStored([]string{"one"}), 0)
	if _, err := decodeStored(body); err == nil {
		t.Fatal("a receipt with trailing bytes was accepted")
	}
}

func TestTakeRefusesMissingOrRepeatedMessageIDs(t *testing.T) {
	for name, messages := range map[string][]convo.Message{
		"missing":  {{ID: ""}},
		"repeated": {{ID: "one"}, {ID: "one"}},
	} {
		t.Run(name, func(t *testing.T) {
			in := messageFrames(t, messages, int64(len(messages)))
			err := Take(wire.NewConn(readWriter{in, &bytes.Buffer{}}), node.ID{}, func(node.ID, convo.Message) error { return nil })
			if err == nil {
				t.Fatal("Take() accepted ambiguous message identity")
			}
		})
	}
}

func TestSendRefusesMissingOrRepeatedMessageIDsBeforeWriting(t *testing.T) {
	for name, messages := range map[string][]convo.Message{
		"missing":  {{ID: ""}},
		"repeated": {{ID: "one"}, {ID: "one"}},
	} {
		t.Run(name, func(t *testing.T) {
			var stream bytes.Buffer
			if _, err := Send(wire.NewConn(readWriter{&stream, &stream}), messages); err == nil {
				t.Fatal("Send() accepted ambiguous message identity")
			}
			if stream.Len() != 0 {
				t.Fatalf("Send() wrote %d bytes before rejecting the batch", stream.Len())
			}
		})
	}
}

func TestSendRefusesAReceiptForAnotherMessage(t *testing.T) {
	var answer bytes.Buffer
	if err := wire.NewConn(readWriter{&answer, &answer}).WriteFrame(wire.KindAck, encodeStored([]string{"not-sent"})); err != nil {
		t.Fatal(err)
	}

	var sent bytes.Buffer
	stored, err := Send(wire.NewConn(readWriter{&answer, &sent}), []convo.Message{{ID: "sent"}})
	if err == nil || len(stored) != 0 {
		t.Fatalf("Send() accepted receipt %v with %v", stored, err)
	}
}

func TestSendAcceptsAPartialReceiptForItsBatch(t *testing.T) {
	var answer bytes.Buffer
	if err := wire.NewConn(readWriter{&answer, &answer}).WriteFrame(wire.KindAck, encodeStored([]string{"one"})); err != nil {
		t.Fatal(err)
	}

	var sent bytes.Buffer
	stored, err := Send(wire.NewConn(readWriter{&answer, &sent}), []convo.Message{{ID: "one"}, {ID: "two"}})
	if err != nil || len(stored) != 1 || stored[0] != "one" {
		t.Fatalf("Send() = %v, %v", stored, err)
	}
}
