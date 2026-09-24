package mobile

import (
	"context"
	"encoding/json"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Manage reads or changes who may reach a path: on this phone when name is empty, and otherwise on
// that machine, which answers only a machine of its own owner's. Op is list, read, level, shown,
// allow, deny or unset; what comes back is every path for list, and the one path for the rest.
func (n *Node) Manage(name, op, path, who, level string, shown bool) (string, error) {
	return n.asked(name, proto.Manage{Op: op, Path: path, Who: who, Level: level, Shown: shown})
}

// asked puts one manage request to this phone, or to the machine of its owner's a name is.
func (n *Node) asked(name string, m proto.Manage) (string, error) {
	more, err := n.more()
	if err != nil {
		return "", err
	}
	var on *book.Entry
	if name != "" {
		entry, err := entry(name)
		if err != nil {
			return "", err
		}
		on = &entry
	}
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()

	out, err := more.Manage(ctx, on, m)
	if err != nil {
		return "", err
	}
	if m.Op != proto.ManageList && m.Op != proto.ManageRead {
		changed(n.events)
	}
	return string(out), nil
}

// Rename files a person, or one machine, under another name here.
func (n *Node) Rename(old, name string) error {
	more, err := n.more()
	if err != nil {
		return err
	}
	return n.changed(more.Rename(old, name))
}

// Reachable is what somebody — a person, or a machine that belongs to nobody — may open on this
// phone and on every other machine of its owner's, as JSON: one entry per machine, the empty one
// being this phone, each with what that machine calls them and every one of its paths.
func (n *Node) Reachable(name string) (string, error) {
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()

	out, err := n.back.Reachable(ctx, name)
	if err != nil {
		return "", err
	}
	return encode(out), nil
}

// AddTopic puts a topic of one kind — chat, inbox, folder, note, terminal, links or stream — on this
// phone when name is empty, or on another machine of its owner's, open only to its owner until they
// say otherwise. A stream takes the command it shows.
func (n *Node) AddTopic(name, path, kind, command string) (string, error) {
	body, err := json.Marshal(proto.TopicBody{Kind: kind, Command: command})
	if err != nil {
		return "", err
	}
	return n.asked(name, proto.Manage{Op: proto.ManageAdd, Path: path, Level: ns.LevelMe, Body: string(body)})
}

// RemoveTopic takes a topic that was added away again.
func (n *Node) RemoveTopic(name, path string) (string, error) {
	return n.asked(name, proto.Manage{Op: proto.ManageRemove, Path: path})
}

// Kinds is every kind a topic can be, as JSON, in the order they are offered.
func (n *Node) Kinds() string { return encode(made.Kinds) }
