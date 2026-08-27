package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"crypto/hmac"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/arch/chat"
	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/arch/share"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/live"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/seen"
	"github.com/bresilla/drop/src/pkg/shares"
	"github.com/bresilla/drop/src/pkg/tui"
)

func runTUI(parent context.Context) error {
	pinned, err := book.Load()
	if err != nil {
		return err
	}

	doing := &doings{pinned: pinned}
	known := doing.serving()
	defer doing.stop()

	cfg, err := conf.Load(known)
	if err != nil {
		return err
	}
	if _, err := cfg.Grants(); err != nil {
		return err
	}
	if err := unlock(cfg); err != nil {
		return err
	}
	cfg.Apply()
	defer cfg.Close()

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	lan, _ := discovery.StartLAN(ctx, n)
	startRendezvous(ctx, n)

	// Depth one, and a full channel is left alone: the signal carries nothing, so one pending
	// knock means the same as ten, and a device that says a great deal at once still redraws once.
	arriving := make(chan struct{}, 1)

	// What arrives while the interface is open belongs in the conversation the same way it would
	// with the daemon running, and the screen is nudged so it is drawn as it happens.
	doing.cfg = cfg
	doing.noticed = func() { knock(arriving) }

	// One connection per device, kept for as long as the interface is open.
	held := dial.Hold(n, lan, finder(n))
	defer held.Close()

	// The interface serves while it is open, so a device that pairs with it can reach it — and
	// so what arrives lands in a conversation rather than being refused.
	answer := map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()

			// Re-read before answering, the way the daemon does. Pairing happens while this is
			// open — from this very interface — and without it a device that just paired stays a
			// stranger until the interface is restarted, which looks exactly like pairing failing.
			_ = pinned.Refresh()

			_ = proto.Handle(ctx, s, from, proto.Policy{
				Mounts:     cfg.Mounts,
				Archetypes: known,
				Allow:      accepting(pinned, false),
				Who:        whoIs(pinned),
				Refused:    noting(pinned),
				Asked:      taking(),
			})
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = pinned.Refresh()

			_ = proto.AnswerHello(s, from, func(badge proto.Badged) proto.Hello {
				return greeting(pinned, cfg.Mounts, known, from, badge)
			})
		},
	}

	// The same as the daemon: answer whatever a device opens on a connection we made, keep the
	// ones it opens to us, and push what is waiting the moment it appears. Without this the
	// interface is only reachable by devices that can be dialled, and every message it sends costs
	// a handshake instead of a stream.
	//
	// A snapshot of its own, never the map the listener is given: the listener adds and removes
	// protocols while this reads, and a map being written to while it is read takes the program
	// down. What a connection we dialled carries is a session or a hello, both of which are here.
	dialled := make(map[string]func(node.ID, *iroh.Stream), len(answer))
	for alpn, handle := range answer {
		dialled[alpn] = handle
	}

	held.Serving(ctx, func(from node.ID, alpn string, s *iroh.Stream) {
		if handle, ok := dialled[alpn]; ok {
			handle(from, s)
		}
	})

	ears := listenKeeping(ctx, n, answer, held, func(from node.ID) {
		_ = pinned.Refresh()

		entry, known := pinned.ByID(from)
		if !known || !entry.Paired() {
			return
		}
		if _, err := deliverOver(ctx, onlyHeld{held: held}, entry, "/chat", "chat"); err == nil {
			knock(arriving)
		}
	})

	go holding(ctx, pinned, held)

	program := tea.NewProgram(
		tui.New(&running{node: n, lan: lan, ears: ears, arriving: arriving, held: held, known: known}),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)

	_, err = program.Run()
	return err
}

// running is the interface's view of a running node.
type running struct {
	ears     *listener
	node     *node.Node
	lan      *discovery.LAN
	arriving chan struct{}
	// held keeps a connection per device, so a conversation costs one handshake rather than one
	// for every line typed.
	held *dial.Kept
	// known is what this machine's own namespaces are, for describing them back to itself.
	known *arch.Registry
}

// Arrivals is how the interface learns that something landed while it was sitting there.
func (l *running) Arrivals() <-chan struct{} { return l.arriving }

// knock says something happened, without ever waiting for anybody to be listening: a device
// sending a file must not be held up because nothing is on screen to care.
func knock(at chan struct{}) {
	select {
	case at <- struct{}{}:
	default:
	}
}

// Reaching is which devices this interface is holding a connection to.
//
// What is held, not what could be reached: dialling the whole address book to draw a list would
// spend a handshake per device per redraw, and a device that answered a moment ago is the useful
// thing to say anyway.
func (l *running) Reaching() map[string]bool {
	pinned, err := book.Load()
	if err != nil {
		return nil
	}

	out := map[string]bool{}
	for _, entry := range pinned.All() {
		if l.held.Reaching(entry.ID) {
			out[entry.Name] = true
		}
	}
	return out
}

func (l *running) Peers() ([]book.Entry, error) {
	pinned, err := book.Load()
	if err != nil {
		return nil, err
	}
	return pinned.All(), nil
}

// Serves asks a device what it shares, and falls back to what it last said.
//
// A device that is off is the ordinary case, not a failure to report as one. What it last shared is
// still the best guess at what it shares, and without it there is no way into a conversation that
// is sitting on this disk. The error comes back as well, so the interface can say the list is from
// memory rather than from the device.
func (l *running) Serves(ctx context.Context, with book.Entry) ([]proto.Served, error) {
	asked, err := l.askShares(ctx, with)
	if err == nil {
		_ = shares.Remember(with.ID, asked)
		return asked, nil
	}

	remembered, kept := shares.Recall(with.ID)
	if kept != nil || len(remembered) == 0 {
		return nil, err
	}
	return remembered, err
}

func (l *running) askShares(ctx context.Context, with book.Entry) ([]proto.Served, error) {
	s, err := l.held.To(ctx, with, node.ALPNHello)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	hello, err := proto.AskHello(s)
	if err != nil {
		return nil, err
	}
	return hello.Serves, nil
}

func (l *running) History(with book.Entry) ([]convo.Message, error) {
	store, err := convo.Open(with.ID)
	if err != nil {
		return nil, err
	}
	return store.History()
}

// Compose writes a message into the conversation. Nothing is dialled: it is a disk write, and the
// interface can draw it before anybody has been reached.
func (l *running) Compose(to book.Entry, body string) error {
	_, err := compose(to, convo.KindText, body, "")
	return err
}

// Deliver sends whatever is queued for a device, over the connection this interface is holding.
func (l *running) Deliver(ctx context.Context, to book.Entry) error {
	_, err := deliverOver(ctx, kept{held: l.held}, to, "/chat", "chat")
	return err
}

// Waiting is which messages are still in the outbox, by id.
func (l *running) Waiting(with book.Entry) (map[string]bool, error) {
	store, err := convo.Open(with.ID)
	if err != nil {
		return nil, err
	}

	queued, err := store.Pending()
	if err != nil {
		return nil, err
	}

	out := make(map[string]bool, len(queued))
	for _, m := range queued {
		out[m.ID] = true
	}
	return out, nil
}

// Mine is what this device serves. No network: it is this machine's own config, and asking the
// wire what this machine shares would be asking somebody else what is in your own pocket.
func (l *running) Mine() ([]proto.Served, error) {
	cfg, err := conf.Load(l.known)
	if err != nil {
		return nil, err
	}
	if _, err := cfg.Grants(); err != nil {
		return nil, err
	}

	// Described as they would be to somebody paired, which is what the list is for: seeing what a
	// device you have paired with would be offered.
	return proto.Describe(cfg.Mounts, l.known, ns.Caller{ID: l.node.ID().String(), Paired: true}), nil
}

// Send copies files to a path on the far device.
//
// The same call the command line makes, over the interface's own node: a long-lived process should
// not stand up a second endpoint to send a file, and the far end should not see a stranger.
func (l *running) Send(ctx context.Context, to book.Entry, path string, files []string, progress func(string, int64, int64)) error {
	sources, err := gather(files, "")
	if err != nil {
		return err
	}

	s, err := l.held.To(ctx, to, node.ALPNSession)
	if err != nil {
		return err
	}
	defer s.Close()

	conn, err := proto.Open(s, path, "share", 0, "", node.DisplayName())
	if err != nil {
		return err
	}
	if err := share.Send(conn, sources, progress); err != nil {
		return err
	}
	for _, src := range sources {
		noteFile(to.ID, convo.Out, src.Name, src.Size)
	}
	return nil
}

// Post sends one message to a path.
func (l *running) Post(ctx context.Context, to book.Entry, path, archetype string, kind byte, body string) error {
	m, err := compose(to, kind, body, "")
	if err != nil {
		return err
	}

	s, err := l.held.To(ctx, to, node.ALPNSession)
	if err != nil {
		return err
	}
	defer s.Close()

	conn, err := proto.Open(s, path, archetype, 0, "", node.DisplayName())
	if err != nil {
		return err
	}
	_, err = chat.Send(conn, []convo.Message{m})
	return err
}

// Watch reads a live path into a screen, nudging the interface whenever the picture changes.
func (l *running) Watch(ctx context.Context, w tui.Watching) error {
	s, err := l.held.To(ctx, w.On, node.ALPNSession)
	if err != nil {
		return err
	}

	conn, err := proto.Open(s, w.Path, w.Archetype, 0, "", node.DisplayName())
	if err != nil {
		return err
	}
	d := live.New(conn, s)
	d.OnResize = func(cols, rows uint16) { w.Sized(int(cols), int(rows)) }

	// The write side stays open. Closing it here used to be how a viewer was kept from typing, but
	// it also threw away the only way to say how big the window is — and what may be typed is the
	// far end's decision, which it makes whatever arrives.
	if w.Ready != nil {
		w.Ready(speaking{d: d})
	}

	done := make(chan error, 1)
	go func() { done <- d.Pump(w.Into) }()

	select {
	case err := <-done:
		return err

	case <-ctx.Done():
		// The stream goes, the connection stays: it is shared with everything else this device is
		// doing, and closing it here would drop a conversation to end a watch.
		s.Close()

		// And the pump is waited for. It writes into a screen the interface is about to take down,
		// and returning while it is still writing leaves two goroutines racing over it — which is
		// a panic on whichever one loses.
		select {
		case <-done:
		case <-time.After(stopWithin):
		}
		return ctx.Err()
	}
}

// stopWithin bounds the wait for a watch to notice its stream has gone. A read already in flight
// lands or fails quickly; anything longer is not worth holding the interface for.
const stopWithin = 2 * time.Second

// speaking is a live path the interface can speak to.
type speaking struct{ d *live.Duplex }

func (s speaking) Resize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return nil
	}
	return s.d.Resize(uint16(cols), uint16(rows))
}

func (s speaking) Type(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	_, err := s.d.Write(p)
	return err
}

var _ = fmt.Sprintf

func (l *running) Self() (tui.Identity, error) {
	// Which process is the node matters to whoever is reading: if the daemon holds the address,
	// this is a view onto something that goes on running after the interface is closed.
	reach := tui.ReachServing
	if !l.node.Own() {
		reach = tui.ReachDaemon
	}

	return tui.Identity{
		Name:  node.DisplayName(),
		ID:    l.node.ID().String(),
		User:  myKey(),
		Reach: reach,
	}, nil
}

// Offer puts this device up for pairing and reports the name it paired with.
//
// The interface shows the ticket and the code while this waits, which is the whole point: a device
// with nothing paired is a dead end, and reaching for a second terminal to fix that is not a design.
func (l *running) Offer(ctx context.Context) (string, <-chan string, error) {
	code, err := proto.NewCode()
	if err != nil {
		return "", nil, err
	}

	invite := ticketFor(l.node.ID(), code)
	done := make(chan string, 1)

	// Findable by whoever holds the ticket, for as long as it is being offered. The rendezvous
	// cannot help: it publishes under a key derived from a shared secret, and pairing is what
	// makes one. Without this a code only ever reaches the same wire.
	if err := node.Findable(ctx, l.node); err != nil {
		return "", nil, err
	}

	// Registered on the interface's own listener rather than starting a second one. Two accept
	// loops on one endpoint race, and the loser hangs up on a connection it does not know.
	l.ears.Handle(node.ALPNPair, func(from node.ID, s *iroh.Stream) {
		defer s.Close()

		p, err := proto.AnswerPairing(s, l.node.ID(), from, node.DisplayName(), written(discovery.LocalAddrs(l.node)))
		if err != nil {
			return
		}
		// The far end has to prove it was given the code, not merely the address.
		if !hmac.Equal(p.Proof, codeProof(code, from, l.node.ID())) {
			return
		}

		// Written down the one way every pairing is written down. A name that is already somebody
		// else's is refused here as it is on the command line, rather than handed, with every rule
		// that mentions it, to whoever paired last.
		name, err := filed(p, "", false)
		if err != nil {
			return
		}

		select {
		case done <- name:
		default:
		}
	})

	go func() {
		<-ctx.Done()
		l.ears.Handle(node.ALPNPair, nil)
	}()

	return invite, done, nil
}

// Join takes a ticket another device is showing.
//
// The same join the command line does, over the endpoint this interface already has up. Not a
// second implementation: when it was one, the two drifted and pairing worked from one and not the
// other.
func (l *running) Join(ctx context.Context, ticket string) (string, error) {
	_, name, err := join(ctx, l.node, l.lan, ticket, "", false, nil)
	return name, err
}

// Holding is what is in one directory of one of this machine's own namespaces.
//
// Read off this disk rather than asked over a wire: it is a directory here, and asking a peer what
// is in your own pocket would be a strange way to find out. The same screen walks it as walks
// somebody else's, so the answer has the same shape.
func (l *running) Holding(path, dir string) ([]tui.Held, error) {
	cfg, err := conf.Load(l.known)
	if err != nil {
		return nil, err
	}
	defer cfg.Close()

	mount, _, ok := cfg.Mounts.Lookup(path)
	if !ok {
		return nil, fmt.Errorf("%s is not a namespace of this machine's", path)
	}

	root := heldIn(mount.Config)
	if root == "" {
		return nil, fmt.Errorf("%s is not a directory of this machine's", path)
	}

	full, err := beneath(root, dir)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", full, err)
	}

	out := make([]tui.Held, 0, len(entries))
	for _, entry := range entries {
		at, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, tui.Held{Name: entry.Name(), Size: at.Size(), At: at.ModTime(), Dir: entry.IsDir()})
	}
	arrange(out)
	return out, nil
}

// heldIn is the directory a namespace of this machine's stands on, and the empty string for one
// that stands on nothing.
//
// The archetypes this process registered that are a directory here. Which they are is this
// process's own business: it is the one that registered them.
func heldIn(cfg arch.Config) string {
	switch held := cfg.(type) {
	case share.Config:
		return held.Dir
	case files.Config:
		return held.Dir
	}
	return ""
}

// beneath is a directory inside a namespace, refusing a name that leaves it.
func beneath(root, dir string) (string, error) {
	for _, part := range strings.Split(dir, "/") {
		if part == ".." {
			return "", fmt.Errorf("%q leaves the namespace", dir)
		}
	}
	return filepath.Join(root, filepath.FromSlash(dir)), nil
}

// arrange puts a listing in the order a directory is read: the ways down first, then by name.
func arrange(held []tui.Held) {
	sort.Slice(held, func(i, j int) bool {
		if held[i].Dir != held[j].Dir {
			return held[i].Dir
		}
		return held[i].Name < held[j].Name
	})
}

// browsing opens a files namespace on another device, over the connection this interface is already
// holding to it.
func (l *running) browsing(ctx context.Context, on book.Entry, path string) (*files.Browsing, func(), error) {
	s, err := l.held.To(ctx, on, node.ALPNSession)
	if err != nil {
		return nil, nil, err
	}

	conn, err := proto.Open(s, path, "files", 0, "", node.DisplayName())
	if err != nil {
		s.Close()
		return nil, nil, err
	}

	walk, err := files.Browse(conn)
	if err != nil {
		s.Close()
		return nil, nil, err
	}

	// The stream goes when the caller is done; the connection stays, because everything else this
	// device is doing is on it.
	return walk, func() { s.Close() }, nil
}

// Listing is what is in a files namespace on another device, at one directory inside it.
func (l *running) Listing(ctx context.Context, on book.Entry, path, dir string) ([]tui.Held, error) {
	walk, done, err := l.browsing(ctx, on, path)
	if err != nil {
		return nil, err
	}
	defer done()

	entries, err := walk.List(dir)
	if err != nil {
		return nil, err
	}

	out := make([]tui.Held, 0, len(entries))
	for _, at := range entries {
		out = append(out, tui.Held{Name: at.Name, Size: at.Size, At: time.Unix(at.At, 0), Dir: at.Dir})
	}
	arrange(out)
	return out, nil
}

// Fetch copies one thing out of a files namespace onto this disk, and says where it landed.
func (l *running) Fetch(ctx context.Context, from book.Entry, path, dir, name string, progress func(string, int64, int64)) (string, error) {
	walk, done, err := l.browsing(ctx, from, path)
	if err != nil {
		return "", err
	}
	defer done()

	// Where anything else arriving would land. A download is a file changing hands, and a second
	// place for it would mean answering "where did it go" twice.
	inbox := conf.Inbox()
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		return "", fmt.Errorf("making %s: %w", inbox, err)
	}

	into := filepath.Join(inbox, filepath.Base(name))
	if err := walk.Get(slashed(dir, name), into, files.Want{Progress: progress}); err != nil {
		return "", err
	}

	if at, err := os.Stat(into); err == nil {
		noteFile(from.ID, convo.In, filepath.Base(name), at.Size())
	}
	return into, nil
}

// Put copies one thing from this disk into a files namespace on another device.
func (l *running) Put(ctx context.Context, to book.Entry, path, dir, from string, progress func(string, int64, int64)) error {
	walk, done, err := l.browsing(ctx, to, path)
	if err != nil {
		return err
	}
	defer done()

	name := filepath.Base(from)
	if err := walk.PutFile(slashed(dir, name), from, progress); err != nil {
		return err
	}

	if at, err := os.Stat(from); err == nil {
		noteFile(to.ID, convo.Out, name, at.Size())
	}
	return nil
}

// Remove deletes one thing from a files namespace on another device.
func (l *running) Remove(ctx context.Context, on book.Entry, path, dir, name string) error {
	walk, done, err := l.browsing(ctx, on, path)
	if err != nil {
		return err
	}
	defer done()

	return walk.Remove(slashed(dir, name))
}

// slashed is one thing inside a namespace, the way the far end names it: slashes, and no leading one.
func slashed(dir, name string) string {
	if dir == "" {
		return name
	}
	return strings.TrimSuffix(dir, "/") + "/" + name
}

// Knocked is what has dialled this device and been turned away.
func (l *running) Knocked() ([]tui.Knock, error) {
	all, err := seen.All()
	if err != nil {
		return nil, err
	}

	out := make([]tui.Knock, 0, len(all))
	for _, at := range all {
		out = append(out, tui.Knock{ID: at.ID.String(), At: at.At, Asked: at.Asked, Why: at.Why})
	}
	return out, nil
}
