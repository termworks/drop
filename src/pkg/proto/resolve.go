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
func wanted(archetype ns.Archetype) byte {
	switch archetype {
	case ns.Share:
		return ModeShare
	case ns.Files:
		return ModeFiles
	case ns.Stream, ns.TTY:
		return ModeDuplex
	case ns.Chat, ns.Link:
		return ModeMessages
	default:
		return 0
	}
}

// resolve finds the namespace an Open is asking for.
func resolve(table *ns.Table, from node.ID, caller ns.Caller, open Open) (Resolved, error) {
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

	if mount.Archetype == ns.Branch {
		return Resolved{}, fmt.Errorf("%s holds other paths but serves nothing itself", mount.Path)
	}
	if want := wanted(mount.Archetype); want != open.Mode {
		return Resolved{}, fmt.Errorf("%s is a %s namespace", mount.Path, mount.Archetype)
	}
	// The tree decides, from the nearest rule above this path. A branch with no type still
	// governs what is under it, which is the whole point of letting one exist.
	if ok, why := table.Admits(mount.Path, caller); !ok {
		return Resolved{}, fmt.Errorf("%s: %s", mount.Path, why)
	}

	return Resolved{Mount: mount, Rest: rest, From: from, Open: open}, nil
}
