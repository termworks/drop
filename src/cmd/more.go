package cmd

import (
	"context"

	"github.com/bresilla/drop/src/pkg/arch/note"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
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
