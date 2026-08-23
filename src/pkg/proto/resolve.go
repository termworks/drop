package proto

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// Resolved is a namespace lookup that succeeded: what is mounted, and what was left of the path.
type Resolved struct {
	Mount ns.Mount
	// Rest is the path below the mount. `/stream` serving `/stream/of/one/thing` gets `/of/one/thing`.
	Rest string
	From node.ID
	Open Open
}

// wanted names the mode a namespace type is served over, so a mismatch is refused with something a
// person can act on rather than a generic decline.
func wanted(kind ns.Kind) byte {
	switch kind {
	case ns.KindFiles:
		return ModeFiles
	case ns.KindStream, ns.KindTTY:
		return ModeDuplex
	case ns.KindChat, ns.KindLink:
		return ModeMessages
	default:
		return 0
	}
}

// resolve finds the namespace an Open is asking for.
func resolve(table *ns.Table, from node.ID, open Open) (Resolved, error) {
	if table == nil {
		return Resolved{}, fmt.Errorf("this node serves no namespaces")
	}

	path := open.Path
	if path == "" {
		path = ns.Root
	}

	mount, rest, found := table.Lookup(path)
	if !found {
		return Resolved{}, fmt.Errorf("nothing is mounted at %s", path)
	}

	if want := wanted(mount.Kind); want != open.Mode {
		return Resolved{}, fmt.Errorf("%s is a %s namespace", mount.Path, mount.Kind)
	}
	if len(mount.Only) > 0 && !allowed(mount.Only, from) {
		return Resolved{}, fmt.Errorf("%s is not open to you", mount.Path)
	}

	return Resolved{Mount: mount, Rest: rest, From: from, Open: open}, nil
}

// allowed reports whether a peer is on a namespace's guest list, by name or by id.
func allowed(only []string, from node.ID) bool {
	id := from.String()
	for _, who := range only {
		if who == id {
			return true
		}
	}
	return false
}
