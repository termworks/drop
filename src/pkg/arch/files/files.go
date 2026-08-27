// Package files is a directory, walked from another machine or held by several of them at once.
//
// Walked, it lists, reads, and — when the mount allows it — writes and removes. One stream carries
// as many request and reply rounds as the caller wants. A round is a request frame and a reply
// frame, and the transfer operations put a run of data frames between them, ending the way every
// transfer in drop ends: a size, a digest, and a verdict.
//
// Held by several machines, it is a folder people share. Each of them signs what somebody did here
// as a change to a history, takes the changes the others made, and works out from all of them what
// the directory now holds — line by line for text, both ways for anything else. The changes say
// which path moved and what it now is; the bytes travel over the same get anybody else reads a file
// with, so a folder shared and a folder browsed are one protocol and not two.
//
// Refused, and said rather than half done: hard links, sparse files, owner, group and extended
// attributes, differences inside a file, three-way merging of anything that is not lines, empty
// directories, and any file that is being written while it is read.
package files

import (
	"context"
	"fmt"
	"github.com/bresilla/drop/src/pkg/weave"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Config is what a files namespace was told: the directory it serves, and whether anything may be
// written into it.
type Config struct {
	Dir      string
	Writable bool
}

// Into is what the process running a files namespace hands it.
type Into struct {
	// Progress, when set, is called as bytes move in either direction.
	Progress func(name string, done, total int64)
	// Landed, when set, is called once an upload is complete and verified.
	Landed func(from node.ID, name string, size int64)
	// Changed, when set, is told that this machine's own change to a shared folder was recorded, so
	// that whoever else holds it hears about it rather than finding out the next time they ask.
	Changed arch.Changed
	// Fetch, when set, gets the bytes of a file a shared folder is missing from whoever has them.
	// Without it a folder keeps its history and writes down what it can, and the files whose bytes
	// are elsewhere wait.
	Fetch func(w Wanted) error
	// Trouble, when set, is told about a folder that cannot be kept level with its history. The
	// same sentence is said once and not again until it changes.
	Trouble func(text string)
}

// Files serves a directory, and keeps the ones several machines hold level with their histories.
type Files struct {
	into Into

	mu sync.Mutex
	// kept is one keeper per namespace path, holding what was last agreed there.
	kept map[string]*keeper
	// said is the last thing said about each namespace, so a timer does not repeat itself.
	said map[string]string
}

func New(into Into) *Files {
	return &Files{into: into, kept: map[string]*keeper{}, said: map[string]string{}}
}

func (f *Files) Name() string { return "files" }
func (f *Files) Version() int { return 1 }

// Read takes the directory a files namespace serves, and whether it takes anything back.
func (f *Files) Read(d arch.Declared) (arch.Config, error) {
	dir, _ := d.String("dir")
	if dir == "" {
		return nil, fmt.Errorf("a files namespace needs a dir")
	}
	writable, _ := d.Bool("writable")
	return Config{Dir: dir, Writable: writable}, nil
}

func (f *Files) Note(c arch.Config) arch.Note {
	cfg, _ := c.(Config)

	detail, about := cfg.Dir+"  read-only", "a directory, to walk through"
	if cfg.Writable {
		detail, about = cfg.Dir+"  read and write", "a directory, to walk through and write in"
	}
	return arch.Note{
		Writable:  cfg.Writable,
		Shareable: true,
		Detail:    detail,
		About:     about,
		Glyph:     "▦",
	}
}

// Serve answers rounds until the caller stops asking.
func (f *Files) Serve(ctx context.Context, at arch.Session) error {
	cfg, ok := at.Config.(Config)
	if !ok || cfg.Dir == "" {
		reject := wire.Reject{Reason: "this namespace has no directory"}
		return at.Conn.WriteFrame(wire.KindReject, reject.Encode())
	}

	// Every name this session is given is resolved through the open directory, one component at a
	// time, and leaves it for nothing: no link out, no dot-dot, and nothing that appears between the
	// check and the open.
	dir, err := os.OpenRoot(cfg.Dir)
	if err != nil {
		reject := wire.Reject{Reason: "this namespace's directory cannot be opened"}
		return at.Conn.WriteFrame(wire.KindReject, reject.Encode())
	}
	defer dir.Close()

	conn := at.Conn
	if err := conn.WriteFrame(wire.KindReply, ready{Writable: cfg.Writable}.encode()); err != nil {
		return err
	}

	for {
		kind, body, err := conn.ReadFrame()
		if err != nil {
			// Closing is how a session ends. There is no goodbye frame: a caller that has finished
			// asking stops, and a stream that ends between rounds ended in the ordinary way.
			if wire.Closed(err) {
				return nil
			}
			return fmt.Errorf("reading a request for %s: %w", at.Path, err)
		}
		if kind != wire.KindRequest {
			return fmt.Errorf("expected a request, got frame kind %d", kind)
		}

		q, err := decodeRequest(body)
		if err != nil {
			return err
		}
		if err := f.answer(conn, at, dir, cfg.Writable, q); err != nil {
			return err
		}
	}
}

// Watch keeps every folder this node holds with somebody else level with its history, until ctx is
// done.
//
// The table is read again every time round, so a namespace created while this is running is picked
// up without anything having to say so. A files namespace nobody else holds is left alone: it is a
// directory this machine serves, and there is nothing for it to be level with.
func (f *Files) Watch(ctx context.Context, mounts *ns.Table) {
	go func() {
		tick := time.NewTicker(Every)
		defer tick.Stop()

		for {
			f.round(mounts)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// round brings every shared folder level with its history once.
func (f *Files) round(mounts *ns.Table) {
	if mounts == nil {
		return
	}

	for _, mount := range mounts.All() {
		if mount.Archetype != f.Name() || !mount.Shared.Declared() {
			continue
		}
		cfg, ok := mount.Config.(Config)
		if !ok || cfg.Dir == "" {
			continue
		}

		made, err := f.keep(mount, cfg)
		if err != nil {
			f.say(mount.Path, fmt.Sprintf("%s: %v", mount.Path, err))
			continue
		}
		f.say(mount.Path, "")
		if made && f.into.Changed != nil {
			f.into.Changed(mount.Path)
		}
	}
}

// keep runs one folder's turn, and says whether a change of this machine's own was recorded.
func (f *Files) keep(mount ns.Mount, cfg Config) (bool, error) {
	k, err := f.keeper(mount, cfg)
	if err != nil {
		return false, err
	}
	return k.once(f.into.Fetch)
}

// keeper is the one keeper for a namespace, made the first time it is wanted and thrown away when
// the directory it was told about changes underneath it.
func (f *Files) keeper(mount ns.Mount, cfg Config) (*keeper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if k, held := f.kept[mount.Path]; held && k.dir == cfg.Dir {
		return k, nil
	}

	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("making %s: %w", cfg.Dir, err)
	}
	l, err := history.Open(mount.Shared.ID())
	if err != nil {
		return nil, err
	}
	k := &keeper{path: mount.Path, dir: cfg.Dir, log: l}
	f.kept[mount.Path] = k
	return k, nil
}

// say reports something about a folder, and only when it is not what was said last time. Empty is
// nothing to report, which is what makes the next real trouble worth saying.
func (f *Files) say(path, text string) {
	f.mu.Lock()
	was := f.said[path]
	f.said[path] = text
	f.mu.Unlock()

	if text == "" || text == was || f.into.Trouble == nil {
		return
	}
	f.into.Trouble(text)
}

// Amiss says whether anything in this folder has a change in it somebody has to settle.
//
// Read off the disk and not out of whatever keeps the folder, so a listing in its own process can
// say it. The walk stops at the first few, because a row has room for a count and not for a list,
// and a folder with a thousand of them is a folder with a problem the count already reports.
func (f *Files) Amiss(c arch.Config) string {
	cfg, ok := c.(Config)
	if !ok || cfg.Dir == "" {
		return ""
	}

	stuck, beside, first := 0, 0, ""
	walk := func(at string, d fs.DirEntry, err error) error {
		switch {
		case err != nil, d.IsDir(), !d.Type().IsRegular():
			return nil
		case stuck+beside >= Enough:
			return fs.SkipAll
		}

		if strings.HasSuffix(at, ".unrecorded") {
			beside++
			return nil
		}
		raw, err := os.ReadFile(at)
		if err != nil || !weave.Textual(raw) {
			return nil
		}
		if who := weave.Unsettled(raw); len(who) > 0 {
			stuck++
			if first == "" {
				rest, err := filepath.Rel(cfg.Dir, at)
				if err != nil {
					rest = filepath.Base(at)
				}
				first = rest
			}
		}
		return nil
	}
	if err := filepath.WalkDir(cfg.Dir, walk); err != nil {
		return ""
	}

	switch {
	case stuck == 1:
		return fmt.Sprintf("unsettled: %s", first)
	case stuck > 1:
		return fmt.Sprintf("unsettled: %s and %d more", first, stuck-1)
	case beside > 0:
		return fmt.Sprintf("%d save(s) their history would not take", beside)
	}
	return ""
}

// Enough is how many unsettled files are looked for before the walk gives up and says "and more".
const Enough = 64
