package cmd

import (
	"context"
	"io"

	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
)

// How something gets a stream to a device: over a connection it is keeping, or by dialling one.
//
// The two differ only in what happens afterwards. A held connection outlives the stream and is the
// point of holding it; a dialled one is finished with when the stream is.
type reaches interface {
	To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, *iroh.Stream, error)
}

// fresh dials every time, which is what a command that runs once and exits should do.
type fresh struct {
	node *node.Node
	lan  *discovery.LAN
}

func (f fresh) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, *iroh.Stream, error) {
	conn, s, err := reach(ctx, f.node, f.lan, entry, alpn)
	if err != nil {
		return nil, nil, err
	}
	return conn, s, nil
}

// kept borrows a connection that is being held, and leaves it open.
type kept struct{ held *dial.Kept }

func (k kept) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, *iroh.Stream, error) {
	s, err := k.held.To(ctx, entry, alpn)
	if err != nil {
		return nil, nil, err
	}
	return staysOpen{}, s, nil
}

// staysOpen is what a held connection hands back in place of itself.
type staysOpen struct{}

func (staysOpen) Close() error { return nil }
