package proto

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/wire"
)

// Open starts a session on a namespace, and answers with the framed stream once the far end has
// accepted it.
//
// Naming an archetype is how a caller says what it expects to find, and is answered plainly when
// the path is something else. The empty name asks for whatever is mounted there, which is what
// somebody who typed a path rather than read a listing is doing.
//
//	conn, err := proto.Open(s, "/work", "files", 0, "", me)
//	b, err := files.Browse(conn)
func Open(s Stream, path, archetype string, version int, secret, from string) (*wire.Conn, error) {
	conn := wire.NewConn(s)

	open := Opening{Archetype: archetype, Version: version, Path: path, From: from, Secret: secret}
	open.Badge, open.Signed = carried()

	if err := conn.WriteFrame(wire.KindOpen, open.encode()); err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading the answer about %s: %w", path, err)
	}
	switch kind {
	case wire.KindAccept:
		return conn, nil
	case wire.KindReject:
		reject, derr := wire.DecodeReject(body)
		if derr != nil {
			return nil, derr
		}
		return nil, Declined{Reason: reject.Reason, Settled: reject.Settled}
	default:
		return nil, fmt.Errorf("expected an answer about %s, got frame kind %d", path, kind)
	}
}
