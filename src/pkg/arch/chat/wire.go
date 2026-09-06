package chat

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/wire"
)

const (
	// MaxBatch caps how many messages one session may carry.
	MaxBatch = 4096
	// MaxBatchBytes caps their encoded size.
	MaxBatchBytes = 16 << 20
)

// Send delivers a batch on an opened namespace and returns the ids the far end stored. Anything not
// in that list stays in the outbox, so a partial delivery is retried rather than lost.
func Send(conn *wire.Conn, batch []convo.Message) ([]string, error) {
	var stored []string
	err := conn.WithReadIdle(wire.FiniteReadIdle, func() error {
		var err error
		stored, err = send(conn, batch)
		return err
	})
	return stored, err
}

func send(conn *wire.Conn, batch []convo.Message) ([]string, error) {
	if len(batch) > MaxBatch {
		return nil, fmt.Errorf("sending %d messages, over the %d limit", len(batch), MaxBatch)
	}
	waiting := make(map[string]bool, len(batch))
	encoded := make([][]byte, 0, len(batch))
	weight := 0
	for _, m := range batch {
		if m.ID == "" {
			return nil, fmt.Errorf("sending a message with no id")
		}
		if waiting[m.ID] {
			return nil, fmt.Errorf("sending message %s twice in one batch", m.ID)
		}
		waiting[m.ID] = true
		body := m.Encode()
		if len(body) > convo.MaxPacked {
			return nil, fmt.Errorf("message %s is %d bytes, over the %d limit", m.ID, len(body), convo.MaxPacked)
		}
		weight += len(body)
		if weight > MaxBatchBytes {
			return nil, fmt.Errorf("sending %d bytes of messages, over the %d limit", weight, MaxBatchBytes)
		}
		encoded = append(encoded, body)
	}
	for _, body := range encoded {
		if err := conn.WriteFrame(wire.KindItem, body); err != nil {
			return nil, err
		}
	}
	if err := conn.WriteFrame(wire.KindEnd, wire.End{Size: int64(len(batch))}.Encode()); err != nil {
		return nil, err
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("waiting for the far end to confirm: %w", err)
	}
	if kind == wire.KindReject {
		reject, derr := wire.DecodeReject(body)
		if derr != nil {
			return nil, derr
		}
		return nil, fmt.Errorf("the messages were refused: %s", reject.Reason)
	}
	if kind != wire.KindAck {
		return nil, fmt.Errorf("expected an ack, got frame kind %d", kind)
	}
	stored, err := decodeStored(body)
	if err != nil {
		return nil, err
	}
	for _, id := range stored {
		if !waiting[id] {
			return nil, fmt.Errorf("the receipt names message %s, which was not sent", id)
		}
		delete(waiting, id)
	}
	return stored, nil
}

// stored is the receipt: which message ids are now on the far end's disk.
func encodeStored(ids []string) []byte {
	w := wire.NewWriter()
	w.Uint(uint64(len(ids)))
	for _, id := range ids {
		w.String(id)
	}
	return w.Body()
}

func decodeStored(body []byte) ([]string, error) {
	r := wire.NewReader(body)
	count, err := r.Uint()
	if err != nil {
		return nil, err
	}
	if count > MaxBatch {
		return nil, fmt.Errorf("receipt claims %d ids, over the %d limit", count, MaxBatch)
	}

	out := make([]string, 0, wire.Hint(count, body, 1))
	for range count {
		id, err := r.String(256)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if !r.Done() {
		return nil, fmt.Errorf("receipt has bytes after its ids")
	}
	return out, nil
}
