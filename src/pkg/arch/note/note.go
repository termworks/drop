// Package note is one file, edited by several people at once.
//
// The file is a real file on this disk, opened in whoever's editor: drop never becomes an editor.
// What it does is notice a save, sign what was saved as a change, and — when changes made on
// another machine arrive — write back the file all the changes together make. So merging happens
// when a file is saved and when something arrives, and not once per keystroke.
//
// A change carries the whole content its author saved, and not a difference against what they
// started from. What somebody saved is a whole file, so that is the one thing known to be true
// about it; a difference would first have to be applied to the version it was taken against before
// the versions could be merged against each other, which is two merges where the job has one. The
// cost is a copy of the file per save, which is what folding a history up later is for.
package note

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// MaxSize is the largest note this keeps. A change carries the whole file, so a file bigger than
// one change can hold is a file that cannot be saved.
const MaxSize = history.MaxBody

// Every is how often each note is held up against its history. A watcher on the directory would
// notice a save sooner and is worth having later; a second is soon enough for somebody typing.
const Every = time.Second

// Config is what a note namespace was told: the file it keeps.
type Config struct {
	File string
}

// Into is what the process running a note hands it.
type Into struct {
	// Changed, when set, is told that this machine's own change was recorded, so that whoever else
	// holds the namespace hears about it rather than finding out the next time they ask.
	Changed arch.Changed
	// Named, when set, says what this machine calls the person who signs with a key. It is what a
	// conflict marker names, so a person reading the file reads names they know.
	Named func(author string) string
	// Trouble, when set, is told about a note that cannot be kept level with its history. The same
	// sentence is said once and not again until it changes.
	Trouble func(text string)
}

// Note keeps a file and a history level with each other.
type Note struct {
	into Into

	mu sync.Mutex
	// kept is one keeper per namespace path, holding what was last written there.
	kept map[string]*keeper
	// said is the last thing said about each namespace, so a timer does not repeat itself.
	said map[string]string
}

func New(into Into) *Note {
	return &Note{into: into, kept: map[string]*keeper{}, said: map[string]string{}}
}

func (n *Note) Name() string { return "note" }
func (n *Note) Version() int { return 1 }

// Read takes the file a note keeps.
func (n *Note) Read(d arch.Declared) (arch.Config, error) {
	file, _ := d.String("file")
	if file == "" {
		return nil, fmt.Errorf("a note namespace needs a file")
	}
	return Config{File: file}, nil
}

func (n *Note) Note(c arch.Config) arch.Note {
	cfg, _ := c.(Config)
	return arch.Note{
		Shareable: true,
		Detail:    cfg.File,
		About:     "a file, written by several people at once",
		Glyph:     "▩",
	}
}

// Serve hands over the note as it stands here.
//
// Catching up is not this: a machine that holds this namespace too opens a meeting, and that is
// answered before any archetype is looked at. What is left for this is somebody who does not hold
// it and wants to read it, and reading it is all they may do — changing it means holding it.
func (n *Note) Serve(ctx context.Context, at arch.Session) error {
	cfg, ok := at.Config.(Config)
	if !ok || cfg.File == "" {
		reject := wire.Reject{Reason: "this namespace has no file"}
		return at.Conn.WriteFrame(wire.KindReject, reject.Encode())
	}

	raw, err := os.ReadFile(cfg.File)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		reject := wire.Reject{Reason: "this namespace's file cannot be read"}
		return at.Conn.WriteFrame(wire.KindReject, reject.Encode())
	}
	if len(raw) > MaxSize {
		reject := wire.Reject{Reason: "this namespace's file is bigger than a note may be"}
		return at.Conn.WriteFrame(wire.KindReject, reject.Encode())
	}

	if err := at.Conn.WriteFrame(wire.KindItem, raw); err != nil {
		return err
	}
	return at.Conn.WriteFrame(wire.KindEnd, wire.End{Size: int64(len(raw))}.Encode())
}

// Text reads a note off an opened namespace.
func Text(conn *wire.Conn) ([]byte, error) {
	kind, body, err := conn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading the note: %w", err)
	}
	switch kind {
	case wire.KindReject:
		reject, err := wire.DecodeReject(body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("the note was refused: %s", reject.Reason)
	case wire.KindItem:
		return body, nil
	}
	return nil, fmt.Errorf("expected a note, got frame kind %d", kind)
}

// Watch keeps every note this node serves level with its history, until ctx is done.
//
// The table is read again every time round, so a namespace created while this is running is picked
// up without anything having to say so.
func (n *Note) Watch(ctx context.Context, mounts *ns.Table) {
	go func() {
		tick := time.NewTicker(Every)
		defer tick.Stop()

		for {
			n.round(mounts)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// round brings every note level with its history once.
func (n *Note) round(mounts *ns.Table) {
	if mounts == nil {
		return
	}

	for _, mount := range mounts.All() {
		if mount.Archetype != n.Name() {
			continue
		}
		cfg, ok := mount.Config.(Config)
		if !ok || cfg.File == "" {
			continue
		}
		if !mount.Shared.Declared() {
			n.say(mount.Path, fmt.Sprintf("%s is a note nobody else holds, so nothing is kept for it", mount.Path))
			continue
		}

		made, err := n.keep(mount, cfg)
		if err != nil {
			n.say(mount.Path, fmt.Sprintf("%s: %v", mount.Path, err))
			continue
		}
		n.say(mount.Path, "")
		if made && n.into.Changed != nil {
			n.into.Changed(mount.Path)
		}
	}
}

// keep runs one note's turn, and says whether a change of this machine's own was recorded.
func (n *Note) keep(mount ns.Mount, cfg Config) (bool, error) {
	k, err := n.keeper(mount, cfg)
	if err != nil {
		return false, err
	}
	return k.once(n.into.Named)
}

// keeper is the one keeper for a namespace, made the first time it is wanted and thrown away when
// the file it was told about changes underneath it.
func (n *Note) keeper(mount ns.Mount, cfg Config) (*keeper, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if k, held := n.kept[mount.Path]; held && k.file == cfg.File {
		return k, nil
	}

	l, err := history.Open(mount.Shared.ID())
	if err != nil {
		return nil, err
	}
	k := &keeper{file: cfg.File, log: l}
	n.kept[mount.Path] = k
	return k, nil
}

// say reports something about a note, and only when it is not what was said last time. Empty is
// nothing to report, which is what makes the next real trouble worth saying.
func (n *Note) say(path, text string) {
	n.mu.Lock()
	was := n.said[path]
	n.said[path] = text
	n.mu.Unlock()

	if text == "" || text == was || n.into.Trouble == nil {
		return
	}
	n.into.Trouble(text)
}
