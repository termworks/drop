// Package made holds the namespaces put up from the command line.
//
// The config is structure: paths, kinds, and the rule somebody wrote by hand for each. This is
// data: one path created with a command and written down so that it is still here after a restart.
// They are kept apart because a program that edits a hand-written file mangles it eventually, and
// because the config is fatal when it does not parse -- a generated Lua file is one unescaped quote
// away from a node that will not start, and quoting does not save it either, because the escapes Go
// writes for a character outside ASCII are ones the Lua reader refuses at compile time. A directory
// with a non-breaking space in its name would brick the config, and a crafted value would rewrite
// the access rule beside it.
//
// The same split as sshd_config and authorized_keys, and it reads the same way: the config says
// what a path is for, this says what has been created since.
package made

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// File is what this is kept in, beside the config and the grants.
const File = "paths.json"

// Entry is one namespace as a command wrote it down.
//
// The declaration, never a Config: what an archetype makes of its settings is opaque to everything
// outside it, so what is stored is the words, and the archetype reads them again on the way back.
type Entry struct {
	// Archetype is what is here, by name, and Version pins which revision of it answers. Zero is
	// whatever is newest, the same as a config that names none.
	Archetype string `json:"type"`
	Version   int    `json:"version,omitempty"`
	// Settings is what that archetype will read, in the three kinds a declaration can hold.
	Settings Settings `json:"settings,omitempty"`
	// Access is who may reach it, in the words a config uses for the same rule.
	Access Access `json:"access"`
	// Shared says several machines hold this one namespace, and what they all call it. It is
	// written down because it is what the history is filed under: a path that came back after a
	// restart under a different name would be a different thing.
	Shared ns.Shared `json:"shared,omitzero"`
}

// Line is one namespace on its way to the node already running: a path, what is there, and whether
// the node should hold it or keep it.
//
// A declaration has whatever keys the archetype reads, so it travels as one JSON object rather than
// as fields with spaces between them.
type Line struct {
	Path string `json:"path"`
	// Keep says this is written down, so the node serves it until it stops rather than for as long
	// as the command that sent it stays connected.
	Keep bool `json:"keep,omitempty"`
	Entry
}

// Store is every created namespace, and the file they are kept in.
type Store struct {
	// One lock, because the node reads this while a `drop path create` elsewhere writes to it.
	mu    sync.RWMutex
	paths map[string]Entry
}

// Path is where the created namespaces are kept.
func Path() (string, error) {
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, File), nil
}

// Load reads what has been created, returning an empty store when there is no file yet.
//
// A file that is there and cannot be read is an error rather than an empty store: a namespace
// somebody wrote down and a namespace that silently went missing look the same from the outside,
// and only one of them is worth being told about.
func Load() (*Store, error) {
	s := &Store{paths: map[string]Entry{}}

	file, err := Path()
	if err != nil {
		return s, err
	}

	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading %s: %w", file, err)
	}

	var onDisk map[string]Entry
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		return s, fmt.Errorf("parsing %s: %w", file, err)
	}

	for at, entry := range onDisk {
		clean, err := ns.Clean(at)
		if err != nil {
			return s, fmt.Errorf("%s: %q is not a path: %w", file, at, err)
		}
		if err := entry.Settings.settle(clean); err != nil {
			return s, fmt.Errorf("%s: %w", file, err)
		}
		s.paths[clean] = entry
	}
	return s, nil
}

// Save writes the created namespaces back, atomically, the way the address book is written.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	file, err := Path()
	if err != nil {
		return err
	}

	raw, err := json.MarshalIndent(s.paths, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the created namespaces: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(file), err)
	}
	return keep.Replace(file, append(raw, '\n'))
}

// change re-reads what is written down, alters it, and writes it back, with nothing else able to
// write in between.
//
// `drop path create` and `drop path rm` are separate processes and the daemon is a third, all
// sharing one file. Read, change, write is three steps, and a writer that lands between the first
// and the third has its namespace thrown away by the third — a path put up and then silently
// missing after an unrelated command, which is the kind of thing nobody thinks to look for.
func (s *Store) change(alter func() bool) error {
	file, err := Path()
	if err != nil {
		return err
	}

	return keep.While(file, func() error {
		fresh, err := Load()
		if err != nil {
			return err
		}

		s.mu.Lock()
		s.paths = fresh.paths
		changed := alter()
		s.mu.Unlock()

		if !changed {
			return nil
		}
		return s.Save()
	})
}

// Add writes a namespace down, replacing whatever was at that path.
func (s *Store) Add(at string, e Entry) error {
	at, err := ns.Clean(at)
	if err != nil {
		return err
	}
	if e.Archetype == "" {
		return fmt.Errorf("%s has no type, so nothing would answer there", at)
	}
	if err := e.Settings.settle(at); err != nil {
		return err
	}

	return s.change(func() bool {
		s.paths[at] = e
		return true
	})
}

// Remove takes a namespace off the list, reporting whether it was on it.
func (s *Store) Remove(at string) (bool, error) {
	at, err := ns.Clean(at)
	if err != nil {
		return false, err
	}

	had := false
	if err := s.change(func() bool {
		_, had = s.paths[at]
		delete(s.paths, at)
		return had
	}); err != nil {
		return false, err
	}
	return had, nil
}

// Get is what was written down at a path, exactly, without the prefix matching a lookup does.
func (s *Store) Get(at string) (Entry, bool) {
	at, err := ns.Clean(at)
	if err != nil {
		return Entry{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.paths[at]
	return e.clone(), ok
}

// Paths is every path that was written down, in the order a person would read them.
func (s *Store) Paths() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0, len(s.paths))
	for at := range s.paths {
		out = append(out, at)
	}
	sort.Strings(out)
	return out
}

// Len is how many namespaces were written down.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.paths)
}

// Skipped is a written-down namespace that was not mounted, and why.
type Skipped struct {
	Path      string
	Archetype string
	// Why is the clause after the semicolon: the reasons have nothing in common but the path.
	Why string
}

// String is the line to print about it.
func (s Skipped) String() string {
	return fmt.Sprintf("%s says %s is a %s; %s", File, s.Path, s.Archetype, s.Why)
}

func (e Entry) clone() Entry {
	out := e
	out.Settings = e.Settings.clone()
	out.Access = e.Access.clone()
	return out
}
