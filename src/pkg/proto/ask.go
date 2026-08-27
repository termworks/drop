package proto

import (
	"context"
	"fmt"

	"github.com/bresilla/drop/src/pkg/wire"
)

// Asking for a path you can see but cannot open.
//
// A visible path is a door with a bell on it. This rings it: it carries the path and whatever the
// caller wants to say about why, and it is answered by being written down for somebody to look at
// later. Nothing is granted here and nothing is opened -- the far end says it heard, and that is
// all the protocol promises.

// MaxWhy bounds what a request may say, because it is somebody else's text landing on a disk.
const MaxWhy = 280

// Ask sends a request to reach a path, and reports what the far end said back.
func Ask(ctx context.Context, s Stream, path, why, from string) error {
	if len(why) > MaxWhy {
		why = why[:MaxWhy]
	}

	conn := wire.NewConn(s)
	open := Opening{Ask: true, Path: path, From: from, Secret: why}
	open.Badge, open.Signed = carried()
	open.Plate, open.Stamped = stamping()
	open.Moved, open.Handed = handing()

	if err := conn.WriteFrame(wire.KindOpen, open.encode()); err != nil {
		return fmt.Errorf("asking for %s: %w", path, err)
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("asking for %s: %w", path, err)
	}
	if kind == wire.KindReject {
		reject, err := wire.DecodeReject(body)
		if err != nil {
			return err
		}
		return Declined{Reason: reject.Reason, Settled: reject.Settled}
	}
	return nil
}

// TakeAsk answers a request: it is heard, written down, and nothing is opened.
func TakeAsk(conn *wire.Conn, policy Policy, from Asker) error {
	if policy.Asked == nil {
		return conn.WriteFrame(wire.KindReject, wire.Reject{Reason: "not taking requests"}.Encode())
	}
	if err := policy.Asked(from); err != nil {
		return conn.WriteFrame(wire.KindReject, wire.Reject{Reason: err.Error()}.Encode())
	}
	return conn.WriteFrame(wire.KindAccept, nil)
}
