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
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
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
// The first line says which it is: "cast", "share <who> <dir>", "pair <code> <name>",
// "via <device> <protocol>", or "held".

// pairHost is the pairing offer open on this node, if any.
//
// One at a time: a second code while the first is unanswered means two ways in, and the person who
// asked for the first one has no way to know the second exists.
type pairHost struct {
	mu   sync.Mutex
	code string
	as   string
	// node is this daemon's endpoint, so a code being shown can publish where to find it.
	node   *node.Node
	paired chan proto.Pairing
}

func newPairHost(n *node.Node) *pairHost { return &pairHost{node: n} }

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
//
// One at a time, the way a pairing code is: two casts on one path are two screens behind one
// address, and whoever is watching has no way to know which of them they were given.
type castHost struct {
	mu     sync.Mutex
	stage  *cast.Caster
	mounts *ns.Table
	// known is what a cast's path is, so the mount it puts up carries the settings the tty
	// archetype reads rather than a shape this file made up.
	known *arch.Registry
	// declared says the path was in the config, so ending a cast leaves it alone. A /cast a person
	// wrote down carries their access rule, and a cast that came and went must not replace it.
	declared bool
}

func newCastHost(mounts *ns.Table, known *arch.Registry) *castHost {
	return &castHost{mounts: mounts, known: known}
}

// live is the cast in progress, or nil when nobody is casting.
func (h *castHost) live() *cast.Caster {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.stage
}

// begin puts a cast on the air, and declares the path it is served at. It refuses while another
// cast is running.
func (h *castHost) begin(cols, rows uint16) (*cast.Caster, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.stage != nil {
		return nil, errors.New("this device is already casting a terminal")
	}
	h.stage = cast.New(cols, rows)

	// The path is put up only when nothing declared it. A config that names /cast already says who
	// may watch it, and overwriting that with "any paired device" hands out a screen its owner
	// meant for one person.
	mount, _, ok := h.mounts.Lookup(CastPath)
	h.declared = ok && mount.Path == CastPath
	if !h.declared {
		_ = h.mounts.Add(castMount(h.known))
	}
	return h.stage, nil
}

// end takes a cast off the air, and the path with it. A cast that has already been replaced ends
// nothing: the one running belongs to whoever started it.
func (h *castHost) end(stage *cast.Caster) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.stage == nil || h.stage != stage {
		return
	}

	h.stage.Stop()
	h.stage = nil
	if !h.declared {
		h.mounts.Drop(CastPath)
	}
}

// shareHost is the handoff open through this node, if any.
//
// One at a time, the way a cast is: two handoffs behind one path are two directories at one
// address, and whoever is sending has no way to know which of them they reached.
type shareHost struct {
	mu     sync.Mutex
	open   *handoff
	mounts *ns.Table
	// known is what the mount a handoff puts up carries, so it holds the settings the share
	// archetype reads rather than a shape this file made up.
	known *arch.Registry
}

// handoff is one handoff on the air, and how whoever asked for it learns it is over.
type handoff struct {
	done chan struct{}
	over bool
	// took says something has actually come through it. A session that landed nothing — one that
	// was refused, or that hung up mid-file — is not the transfer this was put up for.
	took bool
}

func newShareHost(mounts *ns.Table, known *arch.Registry) *shareHost {
	return &shareHost{mounts: mounts, known: known}
}

// begin puts a handoff up, and declares the path it is served at. It refuses while another is
// open, and refuses a path the config declared: that one carries somebody's own rule over their
// own directory, and a handoff that came and went must not stand in for it.
func (h *shareHost) begin(dir string, to []string) (*handoff, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.open != nil {
		return nil, errors.New("this device already has a handoff open")
	}
	if mount, _, ok := h.mounts.Lookup(SharePath); ok && mount.Path == SharePath {
		return nil, fmt.Errorf("the config declares %s already", SharePath)
	}

	mount, err := shareMount(h.known, dir, to)
	if err != nil {
		return nil, err
	}
	if err := h.mounts.Add(mount); err != nil {
		return nil, err
	}

	h.open = &handoff{done: make(chan struct{})}
	return h.open, nil
}

// end takes a handoff down, and the path with it. One that has already been replaced ends nothing:
// the one open belongs to whoever asked for it.
func (h *shareHost) end(box *handoff) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.open == nil || h.open != box {
		return
	}

	h.open = nil
	h.mounts.Drop(SharePath)
}

// took notes that a share namespace received something. A handoff that is open is the one that may
// have taken it.
func (h *shareHost) took() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.open != nil {
		h.open.took = true
	}
}

// finished is a session on some path having ended. A handoff takes one transfer, so once one has
// come through, the one that was open for that path is over.
//
// The path a session named is not the path it was served at: a mount answers for everything under
// it, so /share/anything is the handoff too and has to end it like anything else.
func (h *shareHost) finished(path string) {
	at, err := ns.Clean(path)
	if err != nil {
		return
	}
	mount, _, ok := h.mounts.Lookup(at)
	if !ok || mount.Path != SharePath {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.open == nil || h.open.over || !h.open.took {
		return
	}
	h.open.over = true
	close(h.open.done)
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

const (
	// firstAcceptWait is how long the socket is left alone after one failed accept.
	firstAcceptWait = 10 * time.Millisecond
	// slowestAcceptWait is where the doubling stops. Whatever is wrong is not going to be fixed by
	// asking faster, and a machine that recovers waits at most this long to be noticed.
	slowestAcceptWait = 2 * time.Second
)

// hostLocal listens for whatever on this machine wants to act as this node.
func hostLocal(ctx context.Context, casts *castHost, shares *shareHost, offers *pairHost, held *dial.Kept) error {
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

	// How long to wait after an accept that failed, doubling from there. A machine out of file
	// descriptors fails every accept, and a loop that only ever continues spends a whole core
	// saying so to nobody.
	var waiting time.Duration

	for {
		conn, err := listening.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}

			if waiting == 0 {
				waiting = firstAcceptWait
				fmt.Fprintf(os.Stderr, "drop: cannot accept on %s: %v\n", path, err)
			} else if waiting < slowestAcceptWait {
				waiting *= 2
			}

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(waiting):
			}
			continue
		}

		if waiting != 0 {
			fmt.Fprintf(os.Stderr, "drop: accepting on %s again\n", path)
			waiting = 0
		}

		go func() {
			defer conn.Close()
			if err := takeLocal(ctx, casts, shares, offers, held, conn); err != nil {
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

	stage, err := host.begin(uint16(head.Width), uint16(head.Height))
	if err != nil {
		return err
	}
	defer host.end(stage)

	fmt.Printf("  a terminal is being cast at %s (%dx%d)\n", CastPath, head.Width, head.Height)
	defer fmt.Printf("  the cast at %s ended\n", CastPath)

	return pump(ctx, reader, stage)
}

// takeLocal reads what this connection is for and does it.
func takeLocal(ctx context.Context, casts *castHost, shares *shareHost, offers *pairHost, held *dial.Kept, conn net.Conn) error {
	reading := bufio.NewReader(conn)

	first, err := reading.ReadString('\n')
	if err != nil {
		return err
	}

	what, rest, _ := strings.Cut(strings.TrimSpace(first), " ")
	switch what {
	case "cast":
		return takeCast(ctx, casts, reading)

	case "share":
		return takeShare(ctx, shares, conn, rest)

	case "pair":
		code, as, machine, err := offerAsked(rest)
		if err != nil {
			return err
		}
		return takeOffer(ctx, offers, conn, code, as, machine)

	case "via":
		name, alpn, _ := strings.Cut(rest, " ")
		return takeVia(ctx, held, conn, name, alpn)

	case "held":
		return takeHeld(held, conn)
	}
	return fmt.Errorf("a local connection asked for %q, which is nothing", what)
}

// takeHeld answers with the devices this node has a connection to, one id a line.
//
// Read out of what is already open rather than dialled, so a command asking which of somebody's
// machines to use spends nothing finding out.
func takeHeld(held *dial.Kept, conn net.Conn) error {
	if held == nil {
		return nil
	}

	pinned, err := book.Load()
	if err != nil {
		return err
	}
	for _, entry := range pinned.All() {
		if !held.Reaching(entry.ID) {
			continue
		}
		if _, err := fmt.Fprintln(conn, entry.ID); err != nil {
			return err
		}
	}
	return nil
}

// takeShare holds a handoff open for as long as whoever asked for it stays connected, and takes it
// down as soon as a transfer has come through it.
//
// The line is who may send and then the directory, in that order, because a directory is the one
// field that may have a space in it.
func takeShare(ctx context.Context, host *shareHost, conn net.Conn, rest string) error {
	who, dir, _ := strings.Cut(strings.TrimSpace(rest), " ")
	if dir == "" {
		return errors.New("a handoff with no directory")
	}

	box, err := host.begin(dir, sendersNamed(who))
	if err != nil {
		fmt.Fprintf(conn, "no %v\n", err)
		return nil
	}
	defer host.end(box)

	if _, err := fmt.Fprintln(conn, "ok"); err != nil {
		return err
	}

	fmt.Printf("  a handoff is open at %s, taking things into %s\n", SharePath, dir)
	defer fmt.Printf("  the handoff at %s closed\n", SharePath)

	// Whoever asked going away is what ends it, so an interrupted `drop share` takes the path down
	// rather than leaving a handoff open that nobody is watching.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, conn)
	}()

	select {
	case <-ctx.Done():
	case <-gone:
	case <-box.done:
		fmt.Fprintln(conn, "done")
	}
	return nil
}

// offerAsked reads what a local `drop pair` asked for: a code, a name to file the far end under,
// and whether to keep the device alone rather than the person who owns it.
//
// A dash stands for a name that was not given, so the third field cannot be mistaken for one. Both
// ends of this socket are the same binary, so the line is exactly three fields or it is malformed.
func offerAsked(rest string) (code, as string, machine bool, err error) {
	parts := strings.SplitN(strings.TrimSpace(rest), " ", 3)
	if len(parts) != 3 {
		return "", "", false, fmt.Errorf("a pairing offer asked for %q, which is not a code, a name and a kind", rest)
	}

	code, machine = parts[0], strings.TrimSpace(parts[2]) == "machine"
	if parts[1] != "-" {
		as = parts[1]
	}
	return code, as, machine, nil
}

// takeOffer holds a pairing offer open for as long as whoever asked for it stays connected.
func takeOffer(ctx context.Context, offers *pairHost, conn net.Conn, code, as string, machine bool) error {
	if code == "" {
		return errors.New("a pairing offer with no code")
	}

	waiting, err := offers.open(code, as)
	if err != nil {
		fmt.Fprintf(conn, "busy %v\n", err)
		return nil
	}
	defer offers.close()

	// A context of this offer's own, so that publishing stops when the offer does. Under the
	// daemon's, this device goes on saying where it is for as long as the daemon runs — which is a
	// code that was taken down still telling the world where to find it.
	shown, done := context.WithCancel(ctx)
	defer done()

	// Findable by whoever holds the ticket, for as long as the code is up. The rendezvous cannot
	// help here: it publishes under a key derived from a shared secret, and pairing is what makes
	// one. Without this a code only ever reaches the same wire.
	if err := node.Findable(shown, offers.node); err != nil {
		fmt.Fprintf(os.Stderr, "drop: cannot publish where this device is: %v\n", err)
	}

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
		if err := record(p, as, machine); err != nil {
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
