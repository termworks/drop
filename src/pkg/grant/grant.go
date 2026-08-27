// Package grant holds what the interface has allowed and refused.
//
// The config is structure: paths, kinds, and the rule somebody wrote by hand for each. This is
// data: one name added to a path from a list, one revoked with a keystroke. They are kept apart
// because a program that edits a hand-written file mangles it eventually, and because a refusal
// wants to take effect on the next connection rather than at the next time somebody opens an
// editor.
//
// The same split as sshd_config and authorized_keys, and it reads the same way: the config says
// what a path is for, this says who has been let in and who has been shut out since.
package grant

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// Rule is what has been granted at one path.
type Rule struct {
	// Allow is who may reach it, on top of whatever the config says.
	Allow []string `json:"allow,omitempty"`
	// Deny is who may not, whatever anything else says.
	Deny []string `json:"deny,omitempty"`
}

// Store is every such rule, and the file they are kept in.
type Store struct {
	// One lock, because a serving node reads this from every connection it answers while the
	// interface writes to it, and because Refresh replaces the whole map under them.
	mu    sync.RWMutex
	paths map[string]Rule
	// read is when the file was last written and how big it was, so Refresh can tell whether
	// anything has happened since. Size as well as time, because a grant made and revoked within
	// the same second of a filesystem that counts in seconds would otherwise go unnoticed.
	read time.Time
	size int64
	// broken is why the file last refused to load, and nil once it has loaded. A rule set nobody
	// can read is not a rule set that allows everything.
	broken error
}

func path() (string, error) {
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "grants.json"), nil
}

// Load reads the grants, returning an empty store when there is no file yet.
func Load() (*Store, error) {
	s := &Store{paths: map[string]Rule{}}
	return s, s.reread()
}

// Refresh re-reads the grants if the file has changed since they were loaded.
//
// A running daemon has to notice a grant made somewhere else -- the interface is often a separate
// process, and without this revoking somebody takes effect when the daemon is next restarted,
// which is indistinguishable from revoking not working.
func (s *Store) Refresh() error {
	file, err := path()
	if err != nil {
		return err
	}

	at, err := os.Stat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	s.mu.RLock()
	fresh := at.ModTime().Equal(s.read) && at.Size() == s.size
	s.mu.RUnlock()

	if fresh {
		return nil
	}
	return s.reread()
}

// reread builds the whole rule set before putting any of it in place.
//
// Filling the live map entry by entry and giving up partway through leaves a subset of the
// refusals in force, and nothing says which subset. A revocation that has stopped applying to some
// of the paths it was written for is worse than one that never loaded.
func (s *Store) reread() error {
	file, err := path()
	if err != nil {
		return s.failed(err)
	}

	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return s.failed(nil)
	}
	if err != nil {
		return s.failed(fmt.Errorf("reading %s: %w", file, err))
	}

	var onDisk map[string]Rule
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		return s.failed(fmt.Errorf("parsing %s: %w", file, err))
	}

	fresh := make(map[string]Rule, len(onDisk))
	for at, rule := range onDisk {
		clean, err := ns.Clean(at)
		if err != nil {
			return s.failed(fmt.Errorf("%s: %q is not a path: %w", file, at, err))
		}
		fresh[clean] = rule
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.paths, s.broken = fresh, nil
	if stamp, err := os.Stat(file); err == nil {
		s.read, s.size = stamp.ModTime(), stamp.Size()
	}
	return nil
}

// failed records why the grants could not be read and leaves what was last read in place.
func (s *Store) failed(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.broken = err
	return err
}

// Save writes the grants back.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	file, err := path()
	if err != nil {
		return err
	}

	raw, err := json.MarshalIndent(s.paths, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the grants: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(file), err)
	}
	return keep.Replace(file, append(raw, '\n'))
}

// For reports what has been granted at a path, and at everything above it.
//
// Grants inherit downwards the way a mount does: letting somebody into /work lets them into
// /work/notes, and refusing them at / refuses them everywhere. Unlike an access rule, a grant
// deeper down does not replace what is above it -- there would be no way to write a refusal that
// holds if a grant further in could quietly drop it.
func (s *Store) For(at string) (allow, deny []string) {
	at, err := ns.Clean(at)
	if err != nil {
		return nil, nil
	}

	// Re-read here rather than leaving it to whoever is serving: a refusal that waits for a
	// restart is a refusal that did not happen. It is one stat per session, against a file that
	// changes when somebody presses a key.
	broken := s.Refresh()

	s.mu.RLock()
	defer s.mu.RUnlock()

	if broken == nil {
		broken = s.broken
	}

	for _, above := range ancestry(at) {
		rule, ok := s.paths[above]
		if !ok {
			continue
		}
		allow = append(allow, rule.Allow...)
		deny = append(deny, rule.Deny...)
	}

	// A file nobody can read closes rather than opens. Nothing it might have allowed is handed
	// out, and every refusal that did load stays in force -- the alternative is a revocation that
	// lapses because somebody left a comma in the wrong place.
	if broken != nil {
		return nil, deny
	}
	return allow, deny
}

// Allow adds somebody to a path, and takes them off its refusal list if they were on it.
func (s *Store) Allow(at, who string) error {
	return s.edit(at, func(rule Rule) Rule {
		return Rule{Allow: with(rule.Allow, who), Deny: without(rule.Deny, who)}
	})
}

// Deny refuses somebody at a path, and takes them off its allow list if they were on it.
func (s *Store) Deny(at, who string) error {
	return s.edit(at, func(rule Rule) Rule {
		return Rule{Allow: without(rule.Allow, who), Deny: with(rule.Deny, who)}
	})
}

// Forget drops somebody from a path entirely, leaving whatever the config says about them.
func (s *Store) Forget(at, who string) error {
	return s.edit(at, func(rule Rule) Rule {
		return Rule{Allow: without(rule.Allow, who), Deny: without(rule.Deny, who)}
	})
}

// Paths lists what has been granted anywhere, path order.
func (s *Store) Paths() map[string]Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]Rule, len(s.paths))
	for at, rule := range s.paths {
		out[at] = Rule{Allow: append([]string(nil), rule.Allow...), Deny: append([]string(nil), rule.Deny...)}
	}
	return out
}

func (s *Store) edit(at string, change func(Rule) Rule) error {
	at, err := ns.Clean(at)
	if err != nil {
		return err
	}
	if strings.TrimSpace(at) == "" {
		return fmt.Errorf("a grant needs a path")
	}

	file, err := path()
	if err != nil {
		return err
	}

	// Re-read, change and write back with nothing else able to write in between. The interface
	// grants from one process and `drop path grant` from another, and a decision lost between them
	// is somebody let in who was shut out, or shut out who was let in, with nothing to say so.
	return keep.While(file, func() error {
		if err := s.reread(); err != nil {
			return err
		}

		s.mu.Lock()
		rule := change(s.paths[at])
		if len(rule.Allow) == 0 && len(rule.Deny) == 0 {
			delete(s.paths, at)
		} else {
			s.paths[at] = rule
		}
		s.mu.Unlock()

		return s.Save()
	})
}

// ancestry is a path and every path above it, widest first.
func ancestry(at string) []string {
	out := []string{ns.Root}
	if at == ns.Root {
		return out
	}

	for i, part := range strings.Split(strings.Trim(at, "/"), "/") {
		if i == 0 {
			out = append(out, "/"+part)
			continue
		}
		out = append(out, out[len(out)-1]+"/"+part)
	}
	return out
}

func with(list []string, who string) []string {
	for _, at := range list {
		if at == who {
			return list
		}
	}
	out := append(append([]string(nil), list...), who)
	sort.Strings(out)
	return out
}

func without(list []string, who string) []string {
	var out []string
	for _, at := range list {
		if at != who {
			out = append(out, at)
		}
	}
	return out
}
