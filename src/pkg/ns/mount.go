package ns

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Archetype is what lives at a path, and therefore what happens when someone opens it.
type Archetype byte

const (
	// Share is one-shot: somebody pushes items, they land in a directory, the session ends.
	Share  Archetype = 1
	Stream Archetype = 2
	TTY    Archetype = 3
	Chat   Archetype = 4
	Link   Archetype = 5
	// Branch serves nothing. It exists to carry an access rule for what is under it.
	Branch Archetype = 6
	// Files is a directory the far end walks, reads, and — when the mount allows it — writes to.
	Files Archetype = 7
)

var archetypeNames = map[Archetype]string{
	Share:  "share",
	Files:  "files",
	Stream: "stream",
	TTY:    "tty",
	Chat:   "chat",
	Link:   "link",
	Branch: "branch",
}

func (a Archetype) String() string {
	if name, ok := archetypeNames[a]; ok {
		return name
	}
	return fmt.Sprintf("archetype(%d)", byte(a))
}

// ParseArchetype reads the name a config writes.
func ParseArchetype(name string) (Archetype, error) {
	for archetype, known := range archetypeNames {
		if known == name {
			return archetype, nil
		}
	}

	valid := make([]string, 0, len(archetypeNames))
	for _, known := range archetypeNames {
		valid = append(valid, known)
	}
	sort.Strings(valid)
	return 0, fmt.Errorf("%q is not a namespace type: try %s", name, strings.Join(valid, ", "))
}

// Mount is one namespace this node serves.
type Mount struct {
	Path      string
	Archetype Archetype

	// Dir is a share namespace's drop box, or the directory a files namespace serves.
	Dir string
	// Command is what a stream namespace runs and reads.
	Command string
	// Shell is what a tty namespace starts; empty means $SHELL.
	Shell string
	// Input lets the far end type into a tty namespace.
	Input bool
	// Action is what a link namespace hands a URL to; empty means it only records it.
	Action string
	// Access is who may reach this path and everything under it, until something deeper says
	// otherwise. Undeclared means nobody.
	Access Access
}

// Table is every namespace this node serves.
//
// Keyed by path, so declaring the same path twice replaces rather than duplicates: a config that
// is re-read, or that loops over a list, cannot silently grow the table.
type Table struct {
	// One lock, because a serving node reads this from every connection it answers, and a cast
	// arriving or ending adds and removes a path while they do.
	mu     sync.RWMutex
	mounts map[string]Mount
	// granted is what the interface has allowed and refused, kept apart from the config so that
	// editing one never rewrites the other.
	granted Granting
}

func NewTable() *Table {
	return &Table{mounts: map[string]Mount{}}
}

// Add registers a namespace, replacing whatever was at that path.
func (t *Table) Add(m Mount) error {
	path, err := Clean(m.Path)
	if err != nil {
		return err
	}
	// A mount with no type is a branch: it serves nothing itself and exists to carry an access rule
	// for the paths beneath it. One that is neither a type nor a rule is a typo, and saying so is
	// better than quietly keeping a line that does nothing.
	if _, ok := archetypeNames[m.Archetype]; !ok {
		if !m.Access.Declared() {
			return fmt.Errorf("%s has neither a type nor an access rule, so it does nothing", path)
		}
		m.Archetype = Branch
	}

	m.Path = path

	t.mu.Lock()
	defer t.mu.Unlock()

	t.mounts[path] = m
	return nil
}

// Drop removes a namespace, reporting whether it was there.
//
// For the ones that come and go: a cast is served only while somebody is casting, and a path that
// answers when there is nothing behind it is worse than one that is absent.
func (t *Table) Drop(path string) bool {
	path, err := Clean(path)
	if err != nil {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	_, had := t.mounts[path]
	delete(t.mounts, path)
	return had
}

// Lookup finds who serves a path, and what is left of it.
//
// The longest declared prefix wins, and the remainder is handed to the mount. That is what makes
// `/stream` serve `/stream/of/one/specific/namespace` without declaring every one of them, while a
// more specific mount still takes precedence over a general one.
func (t *Table) Lookup(path string) (Mount, string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	path, err := Clean(path)
	if err != nil {
		return Mount{}, "", false
	}

	best, bestLen, found := Mount{}, -1, false
	for at, m := range t.mounts {
		if !covers(at, path) {
			continue
		}
		if len(at) > bestLen {
			best, bestLen, found = m, len(at), true
		}
	}
	if !found {
		return Mount{}, "", false
	}

	rest := strings.TrimPrefix(path, best.Path)
	if rest == "" {
		rest = Root
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	return best, rest, true
}

// covers reports whether a mount at `at` serves `path`: the same path, or an ancestor of it on a
// segment boundary. Without the boundary check `/stream` would capture `/streaming`.
func covers(at, path string) bool {
	if at == Root {
		return true
	}
	if at == path {
		return true
	}
	return strings.HasPrefix(path, at+"/")
}

// All lists the namespaces, path order.
func (t *Table) All() []Mount {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]Mount, 0, len(t.mounts))
	for _, m := range t.mounts {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Len is how many namespaces are declared.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return len(t.mounts)
}
