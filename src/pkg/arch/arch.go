// Package arch is what a namespace means.
//
// A namespace is an instance: a path, and the name of the archetype it belongs to. An archetype is
// what that instance is — the operations it answers, the settings it reads, the state it keeps.
//
// The rule the rest of drop is held to is one sentence: a namespace knows which archetype it
// belongs to, and does not know what that archetype means. Two namespaces of the same archetype
// are two instances of one thing, the way `friends` and `work` are both chats. Adding a new
// archetype is writing an implementation and registering it — nothing in the namespace layer, the
// config reader, or the wire gains a case.
package arch

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Config is one archetype's settings, as that archetype made them. Opaque everywhere else: the
// namespace holds it and hands it back, and never looks inside.
type Config any

// Declared is a namespace declaration as the config wrote it, before anybody has decided what the
// words mean. An archetype reads its own settings out of it by name; the reader that built it
// knows none of those names.
//
// Each accessor reports whether the declaration mentioned the setting at all, so an archetype can
// tell "off" from "unset".
type Declared interface {
	String(key string) (string, bool)
	Bool(key string) (bool, bool)
	Strings(key string) ([]string, bool)
}

// Note is everything that can be said about a namespace without knowing what it is.
//
// It exists so that a listing, a row in the interface and the startup table can describe a
// namespace of any archetype — including one written next week — without a case for each.
type Note struct {
	// Writable says the far end may put something into this namespace.
	Writable bool
	// Detail is this instance in one column: where it points, what it runs.
	Detail string
	// About is what this archetype is for, in the words of somebody explaining it once.
	About string
	// Glyph is one character, for a list that has no room for a word.
	Glyph string
}

// Stream is what a session runs over: a bidirectional byte stream whose read side can be given a
// deadline, and whose write side can be closed on its own.
type Stream interface {
	io.ReadWriteCloser
	SetReadDeadline(t time.Time) error
}

// Session is one namespace being opened, as it reaches the archetype that serves it.
//
// Everything generic has already happened: the path resolved, the caller was admitted, and the far
// end named this archetype and was told yes. What is said on Conn from here is the archetype's own
// business, and nothing else will read a byte of it.
type Session struct {
	// Path is the namespace, and Rest is whatever was left of the path below it.
	Path string
	Rest string
	// Config is what this archetype made of the declaration.
	Config Config
	// From is the machine on the other end, and Who is what this one knows about it.
	From node.ID
	Who  ns.Caller
	// Conn frames the stream, and Stream is underneath it for a half-close or a deadline.
	Conn   *wire.Conn
	Stream Stream
}

// An Archetype is what a namespace means: the settings it reads, what can be said about it, and
// what happens when somebody opens one.
type Archetype interface {
	// Name is what a config writes and what travels on the wire, and Version is which revision of
	// this archetype's own protocol that name refers to.
	Name() string
	Version() int

	// Read turns a declaration into this archetype's settings, refusing one it cannot serve. It is
	// called once, when the config is read, so a mistake is reported with a file and a line rather
	// than as silence months later.
	Read(Declared) (Config, error)

	// Note is what may be said about a namespace of this archetype.
	Note(Config) Note

	// Serve answers one session. It returns when the session is over.
	Serve(ctx context.Context, at Session) error
}

// Registry is which implementation answers to which name.
//
// A value rather than a package variable: `drop serve` registers everything, `drop chat` registers
// one archetype, and a test registers a fake. Which archetypes a process serves is a property of
// that process.
type Registry struct {
	mu sync.RWMutex
	// by is keyed by name and version, and newest is the version a config gets when it names none.
	by     map[string]Archetype
	newest map[string]int
}

func NewRegistry() *Registry {
	return &Registry{by: map[string]Archetype{}, newest: map[string]int{}}
}

// Register adds an implementation, replacing one already registered under the same name and
// version.
func (r *Registry) Register(a Archetype) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name, version := a.Name(), a.Version()
	r.by[at(name, version)] = a
	if version > r.newest[name] {
		r.newest[name] = version
	}
}

// Lookup finds the implementation for a name. Version zero means whichever is newest, which is what
// a config that names no version is asking for.
func (r *Registry) Lookup(name string, version int) (Archetype, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if version == 0 {
		version = r.newest[name]
	}
	a, ok := r.by[at(name, version)]
	return a, ok
}

// Names is every archetype registered, in the order a person would read them.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, 0, len(r.newest))
	for name := range r.newest {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Missing is what a registry says about a name it does not know, listing what it does.
func (r *Registry) Missing(name string, version int) error {
	known := r.Names()
	if version != 0 {
		if _, ok := r.Lookup(name, 0); ok {
			return fmt.Errorf("there is no %s, only %s", at(name, version), at(name, r.newest[name]))
		}
	}
	return fmt.Errorf("%q is not a namespace type: try %s", name, join(known))
}

func at(name string, version int) string { return fmt.Sprintf("%s/%d", name, version) }

func join(names []string) string {
	switch len(names) {
	case 0:
		return "nothing, because this build registered no archetypes"
	case 1:
		return names[0]
	}
	out := names[0]
	for _, name := range names[1 : len(names)-1] {
		out += ", " + name
	}
	return out + " or " + names[len(names)-1]
}
