package mobile

import (
	"context"
	"time"

	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/proto"
)

// How long a call that reaches another device is given before it gives up.
const (
	reachWithin    = 30 * time.Second
	transferWithin = 30 * time.Minute
)

type served struct {
	Path      string `json:"path"`
	Archetype string `json:"archetype"`
	Shape     string `json:"shape,omitempty"`
	Writable  bool   `json:"writable"`
	Locked    bool   `json:"locked"`
	About     string `json:"about"`
}

type listing struct {
	Paths []served `json:"paths"`
	// Stale says the list is what the device said last time, and why it could not be asked now.
	Stale string `json:"stale,omitempty"`
}

// Paths is what a machine shares with this one. An empty name is this device's own.
func (n *Node) Paths(name string) (string, error) {
	if name == "" {
		mine, err := n.back.Mine()
		if err != nil {
			return "", err
		}
		return encode(listing{Paths: shown(mine)}), nil
	}

	on, err := entry(name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()

	asked, err := n.back.Serves(ctx, on)
	if err != nil && len(asked) == 0 {
		return "", err
	}
	out := listing{Paths: shown(asked)}
	if err != nil {
		out.Stale = err.Error()
	}
	return encode(out), nil
}

func shown(all []proto.Served) []served {
	out := make([]served, 0, len(all))
	for _, s := range all {
		if s.Archetype == "" {
			continue
		}
		out = append(out, served{
			Path: s.Path, Archetype: s.Archetype, Shape: s.Shape,
			Writable: s.Writable, Locked: s.Locked, About: s.About,
		})
	}
	return out
}

type said struct {
	ID      string `json:"id"`
	Out     bool   `json:"out"`
	Kind    string `json:"kind"`
	Body    string `json:"body"`
	Extra   string `json:"extra,omitempty"`
	At      int64  `json:"at"`
	Waiting bool   `json:"waiting"`
}

// History is the conversation with a machine, oldest first.
func (n *Node) History(name string) (string, error) {
	with, err := entry(name)
	if err != nil {
		return "", err
	}
	all, err := n.back.History(with)
	if err != nil {
		return "", err
	}
	waiting, err := n.back.Waiting(with)
	if err != nil {
		return "", err
	}

	out := make([]said, 0, len(all))
	for _, m := range all {
		out = append(out, said{
			ID:      m.ID,
			Out:     m.Dir == convo.Out,
			Kind:    kindOf(m.Kind),
			Body:    m.Body,
			Extra:   m.Extra,
			At:      m.When().UnixMilli(),
			Waiting: waiting[m.ID],
		})
	}
	return encode(out), nil
}

func kindOf(kind byte) string {
	switch kind {
	case convo.KindLink:
		return "link"
	case convo.KindFile:
		return "file"
	case convo.KindEvent:
		return "event"
	default:
		return "text"
	}
}

// Say writes a message into the conversation and sends it.
//
// It returns once the message is on this disk, and the sending goes on behind: a device that is
// off gets it when it next answers, and the screen is told either way.
func (n *Node) Say(name, text string) error {
	to, err := entry(name)
	if err != nil {
		return err
	}
	if err := n.back.Compose(to, text); err != nil {
		return err
	}
	changed(n.events)

	go n.deliver(name)
	return nil
}

// Deliver tries again to send whatever is waiting for a machine.
func (n *Node) Deliver(name string) { go n.deliver(name) }

func (n *Node) deliver(name string) {
	to, err := entry(name)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()

	_ = n.back.Deliver(ctx, to)
	changed(n.events)
}

// Post sends one thing to a path that is not a chat: a link to a link, say.
func (n *Node) Post(name, path, archetype, body string) error {
	to, err := entry(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()

	kind := convo.KindText
	if archetype == "link" {
		kind = convo.KindLink
	}
	err = n.back.Post(ctx, to, path, archetype, kind, body)
	changed(n.events)
	return err
}
