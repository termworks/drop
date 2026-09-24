package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/bresilla/drop/src/pkg/arch/note"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// More is what an interface can do beyond what the full-screen one draws. A phone has room for more
// screens than a terminal pane, and what they need is here rather than in every backend.
type More interface {
	// Note is a shared note, as the far end has it.
	Note(ctx context.Context, on book.Entry, path string) ([]byte, error)
	// Mkdir makes a directory inside a files namespace on another device.
	Mkdir(ctx context.Context, on book.Entry, path, dir, name string) error
	// Move renames something inside a files namespace on another device, within one directory.
	Move(ctx context.Context, on book.Entry, path, dir, from, to string) error
	// Hold takes up a namespace another device holds, at the path it has there, kept under the
	// settings given, and catches up on it. It says where it is held here.
	Hold(ctx context.Context, on book.Entry, path string, settings made.Settings) (string, error)
	// Release stops holding a namespace taken up here. What it left on disk stays.
	Release(at string) error
	// Kept is every namespace taken up here, by path.
	Kept() (map[string]made.Entry, error)
}

var _ More = (*running)(nil)

func (l *running) Note(ctx context.Context, on book.Entry, path string) ([]byte, error) {
	s, done, err := l.open(ctx, on, node.ALPNSession)
	if err != nil {
		return nil, err
	}
	defer done()
	defer stopStreamOnDone(ctx, s)()

	conn, err := proto.Open(s, path, "note", 0, "", node.DisplayName())
	if err != nil {
		return nil, err
	}
	return note.Text(conn)
}

func (l *running) Mkdir(ctx context.Context, on book.Entry, path, dir, name string) error {
	walk, done, err := l.browsing(ctx, on, path)
	if err != nil {
		return err
	}
	defer done()

	return walk.Mkdir(slashed(dir, name))
}

func (l *running) Move(ctx context.Context, on book.Entry, path, dir, from, to string) error {
	walk, done, err := l.browsing(ctx, on, path)
	if err != nil {
		return err
	}
	defer done()

	return walk.Move(slashed(dir, from), slashed(dir, to))
}

// errDaemonHolds is a namespace to take up while the daemon, and not this, serves the machine.
var errDaemonHolds = errors.New("the daemon serves this machine: `drop path join` takes it up there")

// Hold is `drop path join` done by the node this runs: the same checks, written down the same way,
// and put up on this node rather than handed to a daemon.
func (l *running) Hold(ctx context.Context, on book.Entry, path string, settings made.Settings) (string, error) {
	if l.daemon || l.put == nil {
		return "", errDaemonHolds
	}
	at, err := ns.Clean(path)
	if err != nil {
		return "", err
	}

	serves, err := l.askShares(ctx, on)
	if err != nil {
		return "", err
	}
	served, err := joinable(l.known, serves, ns.Address{Machine: on.Name, Path: at})
	if err != nil {
		return "", err
	}
	if err := offered(served, on); err != nil {
		return "", err
	}

	line := made.Line{
		Path: served.Path,
		Keep: true,
		Entry: made.Entry{
			Archetype: served.Archetype,
			Version:   served.Version,
			Settings:  settings,
			Access:    made.Access{Named: []string{personOf(on)}},
			Shared:    served.Shared,
		},
	}
	held, err := taken(l.known, line)
	if err != nil {
		return "", err
	}

	// Up before it is written down, because putting it up is what checks the settings: one written
	// and then refused is a path that is silently not there at the next start.
	if !held {
		if err := l.put.begin(line); err != nil {
			return "", err
		}
		store, err := made.Load()
		if err == nil {
			err = store.Add(line.Path, line.Entry)
		}
		if err != nil {
			l.put.removeWritten(line.Path)
			return "", err
		}
	}

	pinned, err := book.Load()
	if err != nil {
		return "", err
	}
	rule := line.Access.Rule()
	mount := ns.Mount{Path: line.Path, Archetype: line.Archetype, Version: line.Version, Access: rule, Shared: line.Shared}
	if _, err := catchUp(ctx, kept{held: l.held}, on, mount, rule, pinned); err != nil {
		return line.Path, fmt.Errorf("%s is held here; catching up with %s failed: %w", line.Path, on.Name, err)
	}
	return line.Path, nil
}

func (l *running) Release(at string) error {
	at, err := ns.Clean(at)
	if err != nil {
		return err
	}
	store, err := made.Load()
	if err != nil {
		return err
	}
	had, err := store.Remove(at)
	if err != nil {
		return err
	}
	if !had {
		return fmt.Errorf("%s is not something taken up here", at)
	}
	if l.put != nil {
		l.put.removeWritten(at)
	}
	return nil
}

func (l *running) Kept() (map[string]made.Entry, error) {
	store, err := made.Load()
	if err != nil {
		return nil, err
	}
	out := map[string]made.Entry{}
	for _, at := range store.Paths() {
		if e, ok := store.Get(at); ok {
			out[at] = e
		}
	}
	return out, nil
}

// ours is this machine's namespaces the way the node serving them has them: the config, what was
// let in and kept out since, and what was taken up since.
func (l *running) ours() (*conf.Config, error) {
	cfg, err := conf.Load(l.known)
	if err != nil {
		return nil, err
	}
	if err := created(cfg); err != nil {
		cfg.Close()
		return nil, err
	}
	if _, err := cfg.Grants(); err != nil {
		cfg.Close()
		return nil, err
	}
	return cfg, nil
}

// created adds to a config what was taken up, or put up from the command line, since.
func created(cfg *conf.Config) error {
	store, err := made.Load()
	if err != nil {
		return err
	}
	_, err = cfg.Created(store)
	return err
}
