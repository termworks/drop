// Package share is the one-shot push: somebody offers items, they land in a directory, the session
// ends.
//
// Nothing is browsed and nothing is asked for. The sender says what it has, the receiver says how
// much of each it already holds, and then the bytes go over — each item ending with its length and
// its digest, and each answered with a verdict before the next one starts.
package share

import (
	"context"
	"fmt"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Config is what a share namespace was told: where what arrives is put.
type Config struct {
	Dir string
}

// Into is what the process running a share hands it: how to report bytes as they land, and what to
// do about an item once it has.
type Into struct {
	// Progress, when set, is called as bytes land. Total is wire.SizeUnknown for an item with no
	// length.
	Progress func(name string, done, total int64)
	// Landed, when set, is called once an item is complete and verified, under the name it
	// actually took on this disk.
	Landed func(from node.ID, name string, size int64)
}

// Share serves one-shot pushes.
type Share struct {
	into Into
}

func New(into Into) *Share { return &Share{into: into} }

func (s *Share) Name() string { return "share" }
func (s *Share) Version() int { return 1 }

// Read takes the directory a share puts things in.
func (s *Share) Read(d arch.Declared) (arch.Config, error) {
	dir, _ := d.String("dir")
	if dir == "" {
		return nil, fmt.Errorf("a share namespace needs a dir")
	}
	return Config{Dir: dir}, nil
}

func (s *Share) Note(c arch.Config) arch.Note {
	cfg, _ := c.(Config)
	return arch.Note{
		Writable: true,
		Detail:   cfg.Dir,
		About:    "hand files over, once",
		Glyph:    "▣",
	}
}

// Serve takes the items of one push.
func (s *Share) Serve(ctx context.Context, at arch.Session) error {
	cfg, ok := at.Config.(Config)
	if !ok || cfg.Dir == "" {
		reject := wire.Reject{Reason: "this namespace has nowhere to put anything"}
		return at.Conn.WriteFrame(wire.KindReject, reject.Encode())
	}
	return receive(at.Conn, cfg.Dir, at.From, s.into)
}
