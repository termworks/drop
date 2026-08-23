package ns

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is what lives at a path, and therefore what happens when someone opens it.
type Kind byte

const (
	KindFiles  Kind = 1
	KindStream Kind = 2
	KindTTY    Kind = 3
	KindChat   Kind = 4
	KindLink   Kind = 5
	// KindBranch serves nothing. It exists to carry an access rule for what is under it.
	KindBranch Kind = 6
)

var kindNames = map[Kind]string{
	KindFiles:  "files",
	KindStream: "stream",
	KindTTY:    "tty",
	KindChat:   "chat",
	KindLink:   "link",
	KindBranch: "branch",
}

func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("kind(%d)", byte(k))
}

// ParseKind reads the name a config writes.
func ParseKind(name string) (Kind, error) {
	for kind, known := range kindNames {
		if known == name {
			return kind, nil
		}
	}

	valid := make([]string, 0, len(kindNames))
	for _, known := range kindNames {
		valid = append(valid, known)
	}
	sort.Strings(valid)
	return 0, fmt.Errorf("%q is not a namespace type: try %s", name, strings.Join(valid, ", "))
}

// Mount is one namespace this node serves.
type Mount struct {
	Path string
	Kind Kind

	// Dir is where a files namespace writes.
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
	mounts map[string]Mount
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
	if _, ok := kindNames[m.Kind]; !ok {
		if !m.Access.Declared() {
			return fmt.Errorf("%s has neither a type nor an access rule, so it does nothing", path)
		}
		m.Kind = KindBranch
	}

	m.Path = path
	t.mounts[path] = m
	return nil
}

// Lookup finds who serves a path, and what is left of it.
//
// The longest declared prefix wins, and the remainder is handed to the mount. That is what makes
// `/stream` serve `/stream/of/one/specific/namespace` without declaring every one of them, while a
// more specific mount still takes precedence over a general one.
func (t *Table) Lookup(path string) (Mount, string, bool) {
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
	out := make([]Mount, 0, len(t.mounts))
	for _, m := range t.mounts {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Len is how many namespaces are declared.
func (t *Table) Len() int {
	return len(t.mounts)
}
