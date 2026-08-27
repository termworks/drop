package chat

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/wire"
)

// MaxBatch caps how many messages one session may carry, so a peer cannot make the receiver hold an
// unbounded queue in memory.
const MaxBatch = 4096

// Send delivers a batch on an opened namespace and returns the ids the far end stored. Anything not
// in that list stays in the outbox, so a partial delivery is retried rather than lost.
func Send(conn *wire.Conn, batch []convo.Message) ([]string, error) {
	for _, m := range batch {
		if err := conn.WriteFrame(wire.KindItem, m.Encode()); err != nil {
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
	return decodeStored(body)
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
	return out, nil
}
