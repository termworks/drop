package proto

import (
	"errors"
	"fmt"
	"time"

	"github.com/bresilla/drop/src/pkg/ns"
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
	open := Opening{Archetype: archetype, Version: version, Path: path, From: from, Secret: secret}
	return start(s, open)
}

// Meet starts a catch-up on a namespace both machines hold, and answers with the framed stream once
// the far end has accepted it. What is said afterwards is the meet package's business.
//
// The namespace names itself rather than being named by where it is kept here: the far end holds
// the same thing at whatever path it chose, and what the two machines agree on is the name worked
// out from what it was made from.
//
//	conn, err := proto.Meet(s, mount.Shared, me)
//	caught, err := meet.Ask(conn, log, admits)
func Meet(s Stream, held ns.Shared, from string) (*wire.Conn, error) {
	if !held.Declared() {
		return nil, errors.New("meeting about a namespace this machine holds alone")
	}
	return start(s, Opening{Meet: true, Held: held.ID(), From: from})
}

// start writes an opening and waits to be told yes.
//
// The answer is bounded the way the far end bounds the opening it is reading: a peer that takes the
// stream and never says a word otherwise holds this goroutine and its stream until the process
// stops.
func start(s Stream, open Opening) (*wire.Conn, error) {
	conn := wire.NewConn(s)

	what := open.what()
	open.Badge, open.Signed = carried()

	_ = s.SetReadDeadline(time.Now().Add(settleIn))
	defer func() { _ = s.SetReadDeadline(time.Time{}) }()

	if err := conn.WriteFrame(wire.KindOpen, open.encode()); err != nil {
		return nil, fmt.Errorf("opening %s: %w", what, err)
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading the answer about %s: %w", what, err)
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
		return nil, fmt.Errorf("expected an answer about %s, got frame kind %d", what, kind)
	}
}
