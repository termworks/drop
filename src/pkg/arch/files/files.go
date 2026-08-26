// Package files is a directory the far end walks: it lists, reads, and — when the mount allows it —
// writes and removes.
//
// One stream carries as many request and reply rounds as the caller wants. A round is a request
// frame and a reply frame, and the two transfer operations put a run of data frames between them,
// ending the way every transfer in drop ends: a size, a digest, and a verdict.
package files

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
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
}

// Files serves a directory.
type Files struct {
	into Into
}

func New(into Into) *Files { return &Files{into: into} }

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
		Writable: cfg.Writable,
		Detail:   detail,
		About:    about,
		Glyph:    "▦",
	}
}

// Serve answers rounds until the caller stops asking.
func (f *Files) Serve(ctx context.Context, at arch.Session) error {
	cfg, ok := at.Config.(Config)
	if !ok || cfg.Dir == "" {
		reject := wire.Reject{Reason: "this namespace has no directory"}
		return at.Conn.WriteFrame(wire.KindReject, reject.Encode())
	}

	conn := at.Conn
	if err := conn.WriteFrame(wire.KindReply, ready{Writable: cfg.Writable}.encode()); err != nil {
		return err
	}

	for {
		kind, body, err := conn.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
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
		if err := f.answer(conn, at, cfg, q); err != nil {
			return err
		}
	}
}
