// Package chat is a conversation: messages arrive, are stored, and are acknowledged only once they
// are on a disk.
//
// Acknowledging first would turn a crash into silent loss — the sender drops the message from its
// outbox and nobody has it — so a batch is answered with the ids that actually landed, and the rest
// stay queued for the next time.
package chat

import (
	"context"
	"fmt"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Config is what a chat namespace was told, which is nothing: a conversation is with whoever is on
// the other end, and where it is kept is this machine's business.
type Config struct{}

// Into is what the process running a chat hands it: somewhere to put what arrives.
type Into struct {
	// Store puts one arriving message away. Returning an error means it was not stored, and the
	// sender will send it again.
	Store func(from node.ID, m convo.Message) error
}

// Chat serves a conversation.
type Chat struct {
	into Into
}

func New(into Into) *Chat { return &Chat{into: into} }

func (c *Chat) Name() string { return "chat" }
func (c *Chat) Version() int { return 1 }

// Read takes nothing: a chat has nothing to declare.
func (c *Chat) Read(arch.Declared) (arch.Config, error) { return Config{}, nil }

func (c *Chat) Note(arch.Config) arch.Note {
	return arch.Note{
		Writable: true,
		About:    "messages, kept as a conversation",
		Glyph:    "▤",
	}
}

// Serve takes a batch of messages.
func (c *Chat) Serve(ctx context.Context, at arch.Session) error {
	return Take(at.Conn, at.From, c.into.Store)
}

// Take reads a batch, stores each one, and acknowledges only what actually landed.
//
// Exported because a link travels the way a message travels: what differs between the two is what
// the far end does with what arrives, not how it gets there.
func Take(conn *wire.Conn, from node.ID, store func(node.ID, convo.Message) error) error {
	if store == nil {
		return conn.WriteFrame(wire.KindReject, wire.Reject{Reason: "not accepting messages"}.Encode())
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
			if seen > MaxBatch {
				return fmt.Errorf("%s sent more than %d messages in one session", from, MaxBatch)
			}
			m, err := convo.Decode(body)
			if err != nil {
				return fmt.Errorf("reading a message from %s: %w", from, err)
			}
			m.Dir = convo.In
			if err := store(from, m); err != nil {
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
