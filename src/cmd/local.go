package cmd

import (
	"bufio"

	"context"
	"errors"
	"fmt"
	"github.com/tmc/go-iroh/iroh"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bresilla/drop/src/pkg/asciicast"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Anything that needs to *be* this node goes through the node that is already running.
//
// Two processes cannot listen on one address. A cast or a pairing offer that starts its own
// endpoint while `drop serve` holds the port lands on a different one, and whoever dials the
// identity reaches the daemon — which knows nothing about either. So they ask the daemon to do it,
// over a socket on this machine, and there stays one node, one listener, and one address that
// always means the same thing.
//
// The first line says which it is: "cast", "pair <code> <name>", or "via <device> <protocol>".

// pairHost is the pairing offer open on this node, if any.
//
// One at a time: a second code while the first is unanswered means two ways in, and the person who
// asked for the first one has no way to know the second exists.
type pairHost struct {
	mu     sync.Mutex
	code   string
	as     string
	paired chan proto.Pairing
}

func newPairHost() *pairHost { return &pairHost{} }

// open puts a code up for answering, and hands back what to wait on.
func (h *pairHost) open(code, as string) (<-chan proto.Pairing, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.code != "" {
		return nil, errors.New("this device is already showing a code")
	}

	h.code, h.as = code, as
	h.paired = make(chan proto.Pairing, 1)
	return h.paired, nil
}

func (h *pairHost) close() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.code, h.as, h.paired = "", "", nil
}

// asking is the code being offered, and empty when none is.
func (h *pairHost) asking() (string, string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.code, h.as
}

// answered says somebody completed the pairing.
func (h *pairHost) answered(p proto.Pairing) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.paired == nil {
		return
	}
	select {
	case h.paired <- p:
	default:
	}
}

// castHost is the terminal being cast through this node, if any.
type castHost struct {
	mu     sync.Mutex
	stage  *cast.Caster
	mounts *ns.Table
}

func newCastHost(mounts *ns.Table) *castHost {
	return &castHost{mounts: mounts}
}

// live is the cast in progress, or nil when nobody is casting.
func (h *castHost) live() *cast.Caster {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.stage
}

// begin puts a cast on the air, and declares the path it is served at.
func (h *castHost) begin(cols, rows uint16) *cast.Caster {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.stage != nil {
		h.stage.Stop()
	}
	h.stage = cast.New(cols, rows)

	_ = h.mounts.Add(ns.Mount{
		Path:   CastPath,
		Kind:   ns.KindTTY,
		Access: ns.Access{AnyPaired: true},
	})
	return h.stage
}

// end takes it off the air, and the path with it.
func (h *castHost) end() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.stage != nil {
		h.stage.Stop()
		h.stage = nil
	}
	h.mounts.Drop(CastPath)
}

// castSocket is where a cast hands its output to the node.
//
// Named after the identity, so several nodes on one machine — which is what testing drop looks
// like — do not fight over one socket.
func castSocket() (string, error) {
	id, err := node.LocalID()
	if err != nil {
		return "", err
	}

	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		if dir, err = node.ConfigDir(); err != nil {
			return "", err
		}
	} else {
		dir = filepath.Join(dir, "drop")
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "cast-"+node.Brief(id)+".sock"), nil
}

// hostLocal listens for whatever on this machine wants to act as this node.
func hostLocal(ctx context.Context, casts *castHost, offers *pairHost, held *dial.Kept) error {
	path, err := castSocket()
	if err != nil {
		return err
	}

	// A socket left behind by a process that was killed would otherwise make this address
	// permanently unusable.
	_ = os.Remove(path)

	listening, err := net.Listen("unix", path)
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = listening.Close()
		_ = os.Remove(path)
	}()

	for {
		conn, err := listening.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go func() {
			defer conn.Close()
			if err := takeLocal(ctx, casts, offers, held, conn); err != nil {
				fmt.Fprintf(os.Stderr, "drop: %v\n", err)
			}
		}()
	}
}

// takeCast reads one cast from the socket and puts it on the air for as long as it lasts.
func takeCast(ctx context.Context, host *castHost, from io.Reader) error {
	reader, head, err := asciicast.NewReader(from)
	if err != nil {
		return err
	}

	stage := host.begin(uint16(head.Width), uint16(head.Height))
	defer host.end()

	fmt.Printf("  a terminal is being cast at %s (%dx%d)\n", CastPath, head.Width, head.Height)
	defer fmt.Printf("  the cast at %s ended\n", CastPath)

	return pump(ctx, reader, stage)
}

// takeLocal reads what this connection is for and does it.
func takeLocal(ctx context.Context, casts *castHost, offers *pairHost, held *dial.Kept, conn net.Conn) error {
	reading := bufio.NewReader(conn)

	first, err := reading.ReadString('\n')
	if err != nil {
		return err
	}

	what, rest, _ := strings.Cut(strings.TrimSpace(first), " ")
	switch what {
	case "cast":
		return takeCast(ctx, casts, reading)

	case "pair":
		code, as, _ := strings.Cut(rest, " ")
		return takeOffer(ctx, offers, conn, code, as)

	case "via":
		name, alpn, _ := strings.Cut(rest, " ")
		return takeVia(ctx, held, conn, name, alpn)
	}
	return fmt.Errorf("a local connection asked for %q, which is nothing", what)
}

// takeOffer holds a pairing offer open for as long as whoever asked for it stays connected.
func takeOffer(ctx context.Context, offers *pairHost, conn net.Conn, code, as string) error {
	if code == "" {
		return errors.New("a pairing offer with no code")
	}

	waiting, err := offers.open(code, as)
	if err != nil {
		fmt.Fprintf(conn, "busy %v\n", err)
		return nil
	}
	defer offers.close()

	fmt.Println("  showing a pairing code")
	defer fmt.Println("  the pairing code is no longer being shown")

	// Whoever asked going away is what ends the offer, so a cancelled `drop pair` stops this node
	// answering rather than leaving a code live that nobody is watching for.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, conn)
	}()

	select {
	case <-ctx.Done():
		return nil
	case <-gone:
		return nil
	case p := <-waiting:
		if err := record(p, as); err != nil {
			fmt.Fprintf(conn, "failed %v\n", err)
			return err
		}
		fmt.Fprintf(conn, "paired %s %s\n", nameOf(p, as), p.Peer)
		return nil
	}
}

// nameOf is what the far device will be filed under.
func nameOf(p proto.Pairing, as string) string {
	if as != "" {
		return as
	}
	if p.Name != "" {
		return p.Name
	}
	return node.Brief(p.Peer)
}

// takeVia lends a command this node's connection to a device.
//
// The stream is spliced to the socket rather than wrapped in a protocol of its own: what a command
// wants is a stream to somebody, and it already knows what to say over one. So it says it over
// this, and the daemon is a length of pipe rather than a translator that has to be kept in step
// with every protocol drop grows.
func takeVia(ctx context.Context, held *dial.Kept, conn net.Conn, name, alpn string) error {
	if held == nil {
		fmt.Fprintln(conn, "no connections are being held")
		return nil
	}

	pinned, err := book.Load()
	if err != nil {
		fmt.Fprintf(conn, "no %v\n", err)
		return nil
	}

	entry, ok := lookUp(pinned, name)
	if !ok {
		fmt.Fprintf(conn, "no %q is neither a known name nor a peer id\n", name)
		return nil
	}

	s, err := held.To(ctx, entry, alpn)
	if err != nil {
		fmt.Fprintf(conn, "no %v\n", err)
		return nil
	}
	defer s.Close()

	if _, err := fmt.Fprintln(conn, "ok"); err != nil {
		return err
	}
	return splice(conn, s)
}

// splice copies a socket and a stream into each other until both directions have finished.
//
// Each direction ends by closing the write side of the other, so a half-close on either end means
// the same thing it would have meant without a daemon in the middle: no more from me, carry on.
func splice(conn net.Conn, s *iroh.Stream) error {
	done := make(chan error, 2)

	go func() {
		_, err := io.Copy(s, conn)
		s.Close()
		done <- err
	}()
	go func() {
		_, err := io.Copy(conn, s)
		if half, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		}
		done <- err
	}()

	first, second := <-done, <-done
	if first != nil {
		return first
	}
	return second
}

// lookUp finds a device by the name it is filed under, or by its id.
func lookUp(pinned *book.Book, name string) (book.Entry, bool) {
	if entry, ok := pinned.Lookup(name); ok {
		return entry, true
	}

	id, err := node.ParseID(name)
	if err != nil {
		return book.Entry{}, false
	}
	return pinned.ByID(id)
}
