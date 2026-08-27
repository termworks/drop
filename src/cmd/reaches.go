package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
)

// How something gets a stream to a device: over a connection it is keeping, or by dialling one.
//
// The two differ only in what happens afterwards. A held connection outlives the stream and is the
// point of holding it; a dialled one is finished with when the stream is.
type reaches interface {
	To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, proto.Stream, error)
}

// best is how a command should reach a device: through the node already running when there is one,
// and by dialling when there is not.
//
// A command runs once and exits, so everything it does costs a fresh endpoint, a rendezvous lookup,
// a relay session and a handshake — five to ten seconds before a word is sent. The daemon has all
// of that already. Borrowing it turns the same command into a millisecond.
func best(n *node.Node, lan *discovery.LAN) reaches {
	if _, err := castSocket(); err == nil {
		return borrowed{fallback: fresh{node: n, lan: lan}}
	}
	return fresh{node: n, lan: lan}
}

// fresh dials every time, which is what a command that runs once and exits should do.
type fresh struct {
	node *node.Node
	lan  *discovery.LAN
}

func (f fresh) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, proto.Stream, error) {
	conn, s, err := reach(ctx, f.node, f.lan, entry, alpn)
	if err != nil {
		return nil, nil, err
	}
	return conn, s, nil
}

// kept borrows a connection that is being held, and leaves it open.
type kept struct{ held *dial.Kept }

func (k kept) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, proto.Stream, error) {
	s, err := k.held.To(ctx, entry, alpn)
	if err != nil {
		return nil, nil, err
	}
	return staysOpen{}, s, nil
}

// staysOpen is what a held connection hands back in place of itself.
type staysOpen struct{}

func (staysOpen) Close() error { return nil }

// borrowed asks the running node for a stream, and dials for itself if there is nobody to ask.
type borrowed struct{ fallback reaches }

func (b borrowed) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, proto.Stream, error) {
	s, err := viaDaemon(entry, alpn)
	if err == nil {
		return s, s, nil
	}
	if !errors.Is(err, errNoDaemon) {
		// The daemon is there and said no. Dialling around it would take seconds to arrive at the
		// same answer, and would be a second node on one identity while doing it.
		return nil, nil, err
	}
	return b.fallback.To(ctx, entry, alpn)
}

// onlyHeld reaches a device only over a connection already open to it, and refuses otherwise.
//
// What the serving side uses to push. Dialling from there would have two devices that can both dial
// opening connections at each other without end.
type onlyHeld struct{ held *dial.Kept }

func (o onlyHeld) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, proto.Stream, error) {
	s, err := o.held.Existing(ctx, entry, alpn)
	if err != nil {
		return nil, nil, err
	}
	return noClose{}, s, nil
}

// noClose is a closer for a connection that is not ours to close.
type noClose struct{}

func (noClose) Close() error { return nil }

// viaDaemon opens a stream over the running node's connection to a device.
func viaDaemon(entry book.Entry, alpn string) (*lent, error) {
	path, err := castSocket()
	if err != nil {
		return nil, errNoDaemon
	}

	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, errNoDaemon
	}

	if _, err := fmt.Fprintf(conn, "via %s %s\n", entry.Name, alpn); err != nil {
		conn.Close()
		return nil, errNoDaemon
	}

	// One line: whether there is a stream on the other side of this socket now.
	said, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, errNoDaemon
	}

	what, why, _ := strings.Cut(strings.TrimSpace(said), " ")
	if what != "ok" {
		conn.Close()
		return nil, fmt.Errorf("reaching %s: %s", entry.Name, why)
	}
	return &lent{conn}, nil
}

// lent is a stream the daemon is holding on this command's behalf.
//
// Close half-closes, the way a real stream does: the far end reads an end of file and its own
// writes keep working. Done closes the socket, which is what ends the borrowing.
type lent struct{ net.Conn }

func (l *lent) Close() error {
	if half, ok := l.Conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return l.Conn.Close()
}

// Done gives the connection back.
func (l *lent) Done() error { return l.Conn.Close() }
