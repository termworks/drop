package proto

import (
	"context"
	"fmt"
	"io"

	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// ModeMessages carries conversation messages: chat, links, and the notes drop writes about itself.
const ModeMessages byte = 3

// maxBatch caps how many messages one session may carry, so a peer cannot make the receiver hold
// an unbounded queue in memory.
const maxBatch = 4096

// SendMessages delivers a batch and returns the ids the far end stored. Anything not in that list
// stays in the outbox, so a partial delivery is retried rather than lost.
func SendMessages(ctx context.Context, s io.ReadWriteCloser, path string, batch []convo.Message, from string) ([]string, error) {
	if len(batch) == 0 {
		return nil, nil
	}

	conn := wire.NewConn(s)

	open := Open{Mode: ModeMessages, From: from, Path: path}
	if err := conn.WriteFrame(wire.KindOpen, open.encode()); err != nil {
		return nil, err
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading the answer: %w", err)
	}
	if kind == wire.KindReject {
		reject, derr := decodeReject(body)
		if derr != nil {
			return nil, derr
		}
		return nil, fmt.Errorf("declined: %s", reject.Reason)
	}
	if kind != wire.KindAccept {
		return nil, fmt.Errorf("expected an answer, got frame kind %d", kind)
	}

	for _, m := range batch {
		if err := conn.WriteFrame(wire.KindItem, m.Encode()); err != nil {
			return nil, err
		}
	}
	if err := conn.WriteFrame(wire.KindEnd, End{Size: int64(len(batch))}.encode()); err != nil {
		return nil, err
	}

	kind, body, err = conn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("waiting for the far end to confirm: %w", err)
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
	if count > maxBatch {
		return nil, fmt.Errorf("receipt claims %d ids, over the %d limit", count, maxBatch)
	}

	out := make([]string, 0, count)
	for i := uint64(0); i < count; i++ {
		id, err := r.String(256)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// receiveMessages takes a batch, stores each one, and acknowledges only what actually landed.
//
// A message is acknowledged after it is on disk, never before: acknowledging first turns a crash
// into silent loss, because the sender drops it from its outbox and nobody has it.
func receiveMessages(conn *wire.Conn, policy Policy, from node.ID) error {
	if policy.Message == nil {
		return conn.WriteFrame(wire.KindReject, Reject{Reason: "not accepting messages"}.encode())
	}
	if err := conn.WriteFrame(wire.KindAccept, Accept{}.encode()); err != nil {
		return err
	}

	var stored []string
	seen := 0

	for {
		kind, body, err := conn.ReadFrame()
		if err != nil {
			return fmt.Errorf("reading a message from %s: %w", from, err)
		}

		switch kind {
		case wire.KindItem:
			seen++
			if seen > maxBatch {
				return fmt.Errorf("%s sent more than %d messages in one session", from, maxBatch)
			}
			m, err := convo.Decode(body)
			if err != nil {
				return fmt.Errorf("reading a message from %s: %w", from, err)
			}
			m.Dir = convo.In
			if err := policy.Message(from, m); err != nil {
				// Not stored, so not acknowledged: the sender keeps it and tries again.
				continue
			}
			stored = append(stored, m.ID)

		case wire.KindEnd:
			return conn.WriteFrame(wire.KindAck, encodeStored(stored))

		default:
			return fmt.Errorf("%s sent frame kind %d in a message session", from, kind)
		}
	}
}
