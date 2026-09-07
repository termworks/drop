package cmd

import (
	"bufio"

	"context"
	"encoding/json"
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

	"golang.org/x/sys/unix"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/arch/share"
	"github.com/bresilla/drop/src/pkg/asciicast"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/made"
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
// The first line says which it is: "cast", "share <who> <dir>", "mount <declaration>",
// "pair <code> <name>", "via <device> <protocol>", or "held".

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
	lease  ns.Lease
	// known is what a cast's path is, so the mount it puts up carries the settings the tty
	// archetype reads rather than a shape this file made up.
	known *arch.Registry
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
func (h *castHost) begin(cols, rows int) (*cast.Caster, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.stage != nil {
		return nil, errors.New("this device is already casting a terminal")
	}

	mount, lease, reserved := h.mounts.Reserve(CastPath)
	if reserved {
		if mount.Archetype != "tty" {
			lease.Release()
			return nil, fmt.Errorf("%s is already a %s namespace", CastPath, kindOf(mount.Archetype))
		}
	} else {
		var err error
		lease, err = h.mounts.Claim(castMount(h.known))
		if err != nil {
			return nil, fmt.Errorf("putting up %s: %w", CastPath, err)
		}
	}
	h.lease = lease
	h.stage = cast.New(cols, rows)
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
	h.lease.Release()
	h.lease = ns.Lease{}
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
	done   chan struct{}
	over   bool
	config share.Config
	lease  ns.Lease
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
	lease, err := h.mounts.Claim(mount)
	if err != nil {
		return nil, err
	}

	config, ok := mount.Config.(share.Config)
	if !ok {
		lease.Release()
		return nil, fmt.Errorf("the share mount has invalid settings")
	}
	h.open = &handoff{done: make(chan struct{}), config: config, lease: lease}
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
	box.lease.Release()
}

// finished closes the handoff served by one completed share batch.
func (h *shareHost) finished(_ node.ID, path string, config share.Config) {
	at, err := ns.Clean(path)
	if err != nil {
		return
	}
	if at != SharePath && !strings.HasPrefix(at, SharePath+"/") {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.open == nil || h.open.over || h.open.config != config {
		return
	}
	h.open.over = true
	close(h.open.done)
}

// mountHost is the namespaces a command has put up through this node.
//
// Kept by path rather than one at a time, because two commands creating two different paths are two
// perfectly ordinary things happening at once. One path twice is refused: whichever command ended
// first would otherwise take down the mount the other is holding.
type mountHost struct {
	mu     sync.Mutex
	up     map[string]ns.Lease
	mounts *ns.Table
	// known is what a created namespace is, so the mount carries the settings the archetype it
	// names reads rather than a shape this file made up.
	known *arch.Registry
}

func newMountHost(mounts *ns.Table, known *arch.Registry) *mountHost {
	return &mountHost{up: map[string]ns.Lease{}, mounts: mounts, known: known}
}

// begin puts a namespace up and declares the path it is served at. It refuses a path the config
// declares: that one carries somebody's own rule, and a command must not stand in for it.
func (h *mountHost) begin(line made.Line) error {
	at, err := ns.Clean(line.Path)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.up[at]; ok {
		return fmt.Errorf("%s is already up", at)
	}
	if m, _, ok := h.mounts.Lookup(at); ok && m.Path == at {
		switch {
		case m.Source == ns.Configured:
			return fmt.Errorf("the config declares %s already", at)
		// One that was written down is replaced only by another that is written down. A path put
		// up for a moment over one that is meant to outlast it would take it away on the way out.
		case m.Source == ns.Written && !line.Keep:
			return fmt.Errorf("%s is written down already", at)
		}
	}

	answers, ok := h.known.Lookup(line.Archetype, line.Version)
	if !ok {
		return h.known.Missing(line.Archetype, line.Version)
	}
	settings, err := answers.Read(made.Declared(line.Settings))
	if err != nil {
		return err
	}

	// Written when it is in the file, held when it is not. The one that was written down stays
	// after the command that sent it goes, which is what writing it down means.
	source := ns.Held
	if line.Keep {
		source = ns.Written
	}
	mount := ns.Mount{
		Path:      at,
		Source:    source,
		Archetype: line.Archetype,
		Version:   line.Version,
		Config:    settings,
		Access:    line.Access.Rule(),
		Shared:    line.Shared,
	}
	if line.Keep {
		return h.mounts.ReplaceWritten(mount)
	}
	lease, err := h.mounts.Claim(mount)
	if err != nil {
		return err
	}
	h.up[at] = lease
	return nil
}

// end takes a held namespace down, and the path with it.
func (h *mountHost) end(at string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	lease, ok := h.up[at]
	if !ok {
		return
	}
	delete(h.up, at)
	lease.Release()
}

// removeWritten takes an exact written namespace down from the running node.
func (h *mountHost) removeWritten(at string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.mounts.DropIfSource(at, ns.Written)
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
	localDialWithin     = 2 * time.Second
	localHelloWithin    = 10 * time.Second
	maxLocalLine        = 1 << 20
	maxLocalConnections = 64
)

type localClient struct {
	net.Conn
	stop func() bool
}

func (c *localClient) Close() error {
	c.stop()
	return c.Conn.Close()
}

func (c *localClient) CloseWrite() error {
	half, ok := c.Conn.(interface{ CloseWrite() error })
	if !ok {
		return errors.New("local connection cannot close its write side")
	}
	return half.CloseWrite()
}

func dialLocal(ctx context.Context, path string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: localDialWithin}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	client := &localClient{Conn: conn}
	client.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	return client, nil
}

func localGuard(path string) (*os.File, error) {
	guard, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the local socket lock: %w", err)
	}
	if err := unix.Flock(int(guard.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = guard.Close()
		return nil, fmt.Errorf("another node is already serving locally: %w", err)
	}
	return guard, nil
}

type localServer struct {
	path      string
	guard     *os.File
	listening net.Listener
	once      sync.Once
	err       error
}

func openLocalServer(ctx context.Context) (*localServer, error) {
	path, err := castSocket()
	if err != nil {
		return nil, err
	}
	guard, err := localGuard(path)
	if err != nil {
		return nil, err
	}

	_ = os.Remove(path)
	var listen net.ListenConfig
	listening, err := listen.Listen(ctx, "unix", path)
	if err != nil {
		_ = guard.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listening.Close()
		_ = os.Remove(path)
		_ = guard.Close()
		return nil, fmt.Errorf("protecting %s: %w", path, err)
	}

	server := &localServer{path: path, guard: guard, listening: listening}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	return server, nil
}

func (s *localServer) Close() error {
	s.once.Do(func() {
		closeErr := s.listening.Close()
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil
		}
		removeErr := os.Remove(s.path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		s.err = errors.Join(closeErr, removeErr, s.guard.Close())
	})
	return s.err
}

// hostLocal listens for whatever on this machine wants to act as this node.
func hostLocal(ctx context.Context, server *localServer, casts *castHost, shares *shareHost, put *mountHost, offers *pairHost, held *dial.Kept) error {
	var waiting time.Duration
	connections := make(chan struct{}, maxLocalConnections)

	for {
		conn, err := server.listening.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}

			if waiting == 0 {
				fmt.Fprintf(os.Stderr, "drop: cannot accept on %s: %v\n", server.path, err)
			}
			waiting = nextAcceptWait(waiting)
			if !waitForAcceptRetry(ctx, waiting) {
				return nil
			}
			continue
		}

		if waiting != 0 {
			fmt.Fprintf(os.Stderr, "drop: accepting on %s again\n", server.path)
			waiting = 0
		}

		accepted := conn
		if !startBounded(connections, func() {
			defer func() { _ = accepted.Close() }()
			if err := takeLocal(ctx, casts, shares, put, offers, held, accepted); err != nil {
				fmt.Fprintf(os.Stderr, "drop: %v\n", err)
			}
		}) {
			_ = accepted.Close()
		}
	}
}

// takeCast reads one cast from the socket and puts it on the air for as long as it lasts.
func takeCast(ctx context.Context, host *castHost, from io.Reader, conn net.Conn) error {
	if err := conn.SetReadDeadline(time.Now().Add(localHelloWithin)); err != nil {
		return err
	}
	reader, head, err := asciicast.NewReader(from)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = writeLocal(conn, "no %v\n", err)
		return err
	}

	stage, err := host.begin(head.Width, head.Height)
	if err != nil {
		_ = writeLocal(conn, "no %v\n", err)
		return err
	}
	defer host.end(stage)
	if err := writeLocal(conn, "ok\n"); err != nil {
		return err
	}

	fmt.Printf("  a terminal is being cast at %s (%dx%d)\n", CastPath, head.Width, head.Height)
	defer fmt.Printf("  the cast at %s ended\n", CastPath)

	if err := pump(ctx, reader, stage); err != nil {
		return err
	}
	host.end(stage)
	return writeLocal(conn, "done\n")
}

// takeLocal reads what this connection is for and does it.
func takeLocal(ctx context.Context, casts *castHost, shares *shareHost, put *mountHost, offers *pairHost, held *dial.Kept, conn net.Conn) error {
	reading := bufio.NewReader(conn)

	if err := conn.SetReadDeadline(time.Now().Add(localHelloWithin)); err != nil {
		return fmt.Errorf("setting the local request deadline: %w", err)
	}
	first, err := readLocalLine(reading)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return err
	}

	what, rest, _ := strings.Cut(strings.TrimSpace(first), " ")
	switch what {
	case "cast":
		return takeCast(ctx, casts, reading, conn)

	case "share":
		return takeShare(ctx, shares, conn, rest)

	case "mount":
		return takeMount(ctx, put, conn, rest)

	case "unmount":
		return takeUnmount(put, conn, rest)

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

func readLocalLine(reading *bufio.Reader) (string, error) {
	line := make([]byte, 0, min(reading.Size(), maxLocalLine))
	for {
		part, err := reading.ReadSlice('\n')
		if len(line)+len(part) > maxLocalLine {
			return "", fmt.Errorf("a local request is longer than %d bytes", maxLocalLine)
		}
		line = append(line, part...)
		if err == nil {
			return string(line), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return "", err
		}
	}
}

// readLocalReply reads one bounded line under the local handshake deadline.
func readLocalReply(conn net.Conn, reading *bufio.Reader) (string, error) {
	if err := conn.SetReadDeadline(time.Now().Add(localHelloWithin)); err != nil {
		return "", err
	}
	line, err := readLocalLine(reading)
	_ = conn.SetReadDeadline(time.Time{})
	return line, err
}

func writeLocal(conn net.Conn, format string, args ...any) (err error) {
	if err := conn.SetWriteDeadline(time.Now().Add(localHelloWithin)); err != nil {
		return err
	}
	defer func() {
		reset := conn.SetWriteDeadline(time.Time{})
		if errors.Is(reset, net.ErrClosed) || errors.Is(reset, io.ErrClosedPipe) {
			reset = nil
		}
		err = errors.Join(err, reset)
	}()
	_, err = fmt.Fprintf(conn, format, args...)
	return err
}

// takeHeld answers with the devices this node has a connection to, one id a line.
//
// Read out of what is already open rather than dialled, so a command asking which of somebody's
// machines to use spends nothing finding out.
func takeHeld(held *dial.Kept, conn net.Conn) error {
	if held != nil {
		pinned, err := book.Load()
		if err != nil {
			return err
		}
		for _, entry := range pinned.All() {
			if !held.Reaching(entry.ID) {
				continue
			}
			if err := writeLocal(conn, "%s\n", entry.ID); err != nil {
				return err
			}
		}
	}
	return writeLocal(conn, "%s\n", heldReplyEnd)
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
		return writeLocal(conn, "no %v\n", err)
	}
	defer host.end(box)

	if err := writeLocal(conn, "ok\n"); err != nil {
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
		if err := writeLocal(conn, "done\n"); err != nil {
			return err
		}
	}
	return nil
}

// takeMount puts a created namespace up, and holds it for as long as whoever asked for it stays
// connected — unless it was written down, in which case it is served until this node stops.
//
// The rest of the line is one JSON object: a declaration has whatever keys the archetype reads, and
// both ends of this socket are the same binary.
func takeMount(ctx context.Context, host *mountHost, conn net.Conn, rest string) error {
	var line made.Line
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest)), &line); err != nil {
		return fmt.Errorf("reading the namespace to put up: %w", err)
	}

	if err := host.begin(line); err != nil {
		return writeLocal(conn, "no %v\n", err)
	}
	if err := writeLocal(conn, "ok\n"); err != nil {
		host.end(line.Path)
		return err
	}

	if line.Keep {
		fmt.Printf("  %s is up, and written down\n", line.Path)
		return nil
	}
	defer host.end(line.Path)

	fmt.Printf("  %s is up while whoever asked for it is here\n", line.Path)
	defer fmt.Printf("  %s is gone\n", line.Path)

	// Whoever asked going away is what takes it down, so an interrupted `drop path create` leaves
	// nothing behind that answers when there is nothing behind it.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, conn)
	}()

	select {
	case <-ctx.Done():
	case <-gone:
	}
	return nil
}

// takeUnmount takes down a namespace this node put up because it was written down.
//
// Only one it put up itself: a path the config declares is not this node's to drop, and one another
// command is holding goes when that command does.
func takeUnmount(host *mountHost, conn net.Conn, rest string) error {
	at, err := ns.Clean(strings.TrimSpace(rest))
	if err != nil {
		return writeLocal(conn, "no %v\n", err)
	}

	if host.removeWritten(at) {
		fmt.Printf("  %s is gone\n", at)
		return writeLocal(conn, "ok\n")
	}
	if m, _, ok := host.mounts.Lookup(at); ok && m.Path == at && m.Source == ns.Held {
		return writeLocal(conn, "no something is holding that open\n")
	}
	return writeLocal(conn, "no this node did not put that up\n")
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
		return writeLocal(conn, "busy %v\n", err)
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
			_ = writeLocal(conn, "failed %v\n", err)
			return err
		}
		return writeLocal(conn, "paired %s %s\n", nameOf(p, as), p.Peer)
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
		return writeLocal(conn, "no connections are being held\n")
	}

	pinned, err := book.Load()
	if err != nil {
		return writeLocal(conn, "no %v\n", err)
	}

	entry, ok := lookUp(pinned, name)
	if !ok {
		return writeLocal(conn, "no %q is neither a known name nor a peer id\n", name)
	}

	s, err := held.To(ctx, entry, alpn)
	if err != nil {
		return writeLocal(conn, "no %v\n", err)
	}
	defer func() { _ = s.Close() }()

	if err := writeLocal(conn, "ok\n"); err != nil {
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
		_ = s.Close()
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
