package mobile

import (
	"context"

	"github.com/bresilla/drop/src/cmd"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Manage reads or changes who may reach a path: on this phone when name is empty, and otherwise on
// that machine, which answers only a machine of its own owner's. Op is list, read, level, shown,
// allow, deny or unset; what comes back is every path for list, and the one path for the rest.
func (n *Node) Manage(name, op, path, who, level string, shown bool) (string, error) {
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

	out, err := more.Manage(ctx, on, proto.Manage{Op: op, Path: path, Who: who, Level: level, Shown: shown})
	if err != nil {
		return "", err
	}
	if op != proto.ManageList && op != proto.ManageRead {
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

// Leave takes this phone back out of whoever's machines it became. It is its own from the next start.
func Leave() error { return cmd.Leave() }
