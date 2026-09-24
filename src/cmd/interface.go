package cmd

import (
	"context"
	"errors"

	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/tui"
)

// Hooks is what whoever runs an interface hears as things happen.
type Hooks struct {
	// Trouble reports something that went wrong without stopping anything.
	Trouble func(text string)
	// Said is told about each message that lands, with the name its sender is filed under here.
	Said func(from string, m convo.Message)
	// Landed is told about each file that arrives, with who sent it and what it is called.
	Landed func(from, name string, size int64)
}

// Interface brings up a node for somebody to look at: it serves for as long as it is up, keeps a
// connection to each device it reaches, and pushes what is queued the moment it can.
//
// What the full-screen interface runs on, and what an app does. One of these, not one per front
// end: when the two were separate they drifted, and a thing that worked from one did not from the
// other. What comes back takes the whole of it down.
func Interface(ctx context.Context, hooks Hooks) (tui.Backend, func(), error) {
	var undo []func()
	down := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}

	// The command line does this before any command runs. An app has no command line, and without
	// it a device that has never run one pairs as nobody's and presents nothing to prove it paired.
	if err := prepare(); err != nil {
		return nil, nil, err
	}

	pinned, err := book.Load()
	if err != nil {
		return nil, nil, err
	}

	doing := &doings{pinned: pinned, trouble: hooks.Trouble}
	known := doing.serving()
	undo = append(undo, doing.stop)

	cfg, err := conf.Load(known)
	if err != nil {
		down()
		return nil, nil, err
	}
	undo = append(undo, cfg.Close)
	if _, err := cfg.Grants(); err != nil {
		down()
		return nil, nil, err
	}
	store, err := made.Load()
	if err != nil {
		down()
		return nil, nil, err
	}
	skipped, err := cfg.Created(store)
	if err != nil {
		down()
		return nil, nil, err
	}
	for _, one := range skipped {
		doing.warn(one.String())
	}
	if err := unlock(cfg); err != nil {
		down()
		return nil, nil, err
	}
	cfg.Apply()

	ctx, cancel := context.WithCancel(ctx)
	undo = append(undo, cancel)

	// Depth one, and a full channel is left alone: the signal carries nothing, so one pending
	// knock means the same as ten, and a device that says a great deal at once still redraws once.
	arriving := make(chan struct{}, 1)

	// With the daemon up, this is a view onto it and nothing more: no endpoint of its own. A second
	// endpoint under the same identity, however briefly, announces itself on the wire, publishes
	// where it is and takes over the relay's route to this identity — after which the far end's
	// answers to the daemon go to a port nobody is reading, and the daemon cannot reach anybody.
	if daemonUp(ctx) {
		id, err := node.LocalID()
		if err != nil {
			down()
			return nil, nil, err
		}
		go hearDaemon(ctx, arriving)
		return &running{id: id, daemon: true, arriving: arriving, known: known}, down, nil
	}

	n, err := node.Start(ctx)
	if err != nil {
		down()
		return nil, nil, err
	}
	undo = append(undo, func() { _ = n.Close() })

	lan, _ := discovery.StartLAN(ctx, n)
	startRendezvous(ctx, n)

	// What arrives while the interface is open belongs in the conversation the same way it would
	// with the daemon running, and the screen is nudged so it is drawn as it happens.
	doing.cfg = cfg
	doing.noticed = func() { knock(arriving) }
	if hooks.Said != nil {
		doing.said = func(from node.ID, m convo.Message) { hooks.Said(nameFor(pinned, from), m) }
	}
	if hooks.Landed != nil {
		doing.arrived = func(from node.ID, name string, size int64) { hooks.Landed(nameFor(pinned, from), name, size) }
	}

	// One connection per device, kept for as long as the interface is open.
	held := dial.Hold(n, lan, finder(n))
	undo = append(undo, held.Close)

	// What several machines hold is kept level while this is open, the same as the daemon keeps
	// it: a note saved here goes out, one saved there comes in, and a folder follows along.
	doing.changed = told(ctx, kept{held: held}, cfg.Mounts, pinned)
	doing.pulls = fetching(ctx, kept{held: held}, cfg.Mounts, pinned)
	notesStopped := doing.noting().Watch(ctx, cfg.Mounts)
	filesStopped := doing.filing().Watch(ctx, cfg.Mounts)
	undo = append(undo, func() {
		cancel()
		<-notesStopped
		<-filesStopped
	})
	put := newMountHost(cfg.Mounts, known)

	// The interface serves while it is open, so a device that pairs with it can reach it — and
	// so what arrives lands in a conversation rather than being refused.
	answer := map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer func() { _ = s.Close() }()

			// Re-read before answering, the way the daemon does. Pairing happens while this is
			// open — from this very interface — and without it a device that just paired stays a
			// stranger until the interface is restarted, which looks exactly like pairing failing.
			if err := pinned.Refresh(); err != nil {
				return
			}

			_ = proto.Handle(ctx, s, from, proto.Policy{
				Mounts:     cfg.Mounts,
				Archetypes: known,
				Allow:      accepting(pinned, false),
				Who:        whoIs(pinned),
				Moved:      moving(pinned, func(string) {}),
				Refused:    noting(pinned),
				Asked:      taking(),
				Met:        meeting(cfg.Mounts, pinned, doing.changed),
			})
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer func() { _ = s.Close() }()
			if err := pinned.Refresh(); err != nil {
				return
			}

			_ = proto.AnswerHello(s, from, func(badge proto.Badged) proto.Hello {
				return greeting(pinned, cfg.Mounts, known, from, badge)
			}, moving(pinned, func(string) {}))
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
		if err := pinned.Refresh(); err != nil {
			return
		}

		entry, known := pinned.ByID(from)
		if !known || !entry.Paired() {
			return
		}
		pushTo(ctx, onlyHeld{held: held}, entry, cfg.Mounts, pinned)
		knock(arriving)
	})

	go holding(ctx, pinned, held)

	// With the daemon holding the address, what arrives lands there rather than here.
	if !n.Own() {
		go hearDaemon(ctx, arriving)
	}

	return &running{node: n, id: n.ID(), lan: lan, ears: ears, arriving: arriving, held: held, known: known, put: put}, down, nil
}

// Entry finds somebody in the address book by the name they are filed under, or by their id.
func Entry(name string) (book.Entry, error) {
	pinned, err := book.Load()
	if err != nil {
		return book.Entry{}, err
	}
	entry, ok := lookUp(pinned, name)
	if !ok {
		return book.Entry{}, errors.New(name + " is not in the address book")
	}
	return entry, nil
}

// daemonUp says whether this identity's daemon is running on this machine and answering it.
func daemonUp(ctx context.Context) bool { return heldHere(ctx) != nil }
