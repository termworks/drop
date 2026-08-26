package cmd

import (
	"context"
	"crypto/hmac"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

func newServeCmd() *cobra.Command {
	var quiet bool

	cmd := &cobra.Command{
		Use:     "serve",
		Aliases: []string{"daemon"},
		Short:   "Serve the namespaces this node declares, and stay reachable",
		Long: "What is served comes from the config, not from flags. Every namespace it declares is\n" +
			"available to paired devices at <this node>/<path>.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), quiet)
		},
	}

	cmd.Flags().BoolVar(&quiet, "quiet", false, "only report arrivals and errors")

	return cmd
}

func runServe(parent context.Context, quiet bool) error {
	pinned, err := book.Load()
	if err != nil {
		return err
	}

	// The archetypes come first: reading a config is what needs them, because each mount is read
	// by the archetype it names.
	bar := &progress{}
	doing := &doings{
		pinned: pinned,
		bar:    bar,
		notes:  func(text string) { fmt.Printf("  %s\n", text) },
	}
	known := doing.serving()
	defer doing.stop()

	cfg, err := conf.Load(known)
	if err != nil {
		return err
	}
	doing.cfg = cfg
	if _, err := cfg.Grants(); err != nil {
		return err
	}
	created, err := made.Load()
	if err != nil {
		return err
	}
	skipped, err := cfg.Created(created)
	if err != nil {
		return err
	}
	if err := unlock(cfg); err != nil {
		return err
	}
	// Settings take effect before the endpoint starts, because the name is read while it comes up.
	cfg.Apply()
	defer cfg.Close()

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	startRendezvous(ctx, n)

	lan, err := discovery.StartLAN(ctx, n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: mDNS unavailable: %v\n", err)
	}

	// Connections to every paired device, held for as long as this runs, so nothing anybody sends
	// has to wait for a device to be found first.
	held := dial.Hold(n, lan, finder(n))
	defer held.Close()

	go keepConnected(ctx, held, pinned)
	go backlog(ctx, pinned, held, cfg.Mounts)

	// What an archetype calls when something in one of its namespaces moves. Set here rather than
	// where the archetypes were registered, because reaching the machines that hold a namespace
	// needs the connections and the address book, and neither exists until the config is read.
	doing.changed = told(ctx, kept{held: held}, cfg.Mounts, pinned)

	// A cast feeds this node over a local socket rather than standing up a second one, so a
	// terminal can be shared while the daemon is running.
	casts := newCastHost(cfg.Mounts, known)
	// A handoff is put up the same way: mounted while somebody is waiting for a file, and gone
	// again the moment they are not.
	shares := newShareHost(cfg.Mounts, known)
	doing.took = shares.took
	doing.shown = func(path string) (*cast.Caster, bool) {
		if path != CastPath {
			return nil, false
		}
		return casts.live(), true
	}
	// And a namespace created from the command line: written down and served until this stops, or
	// held up for as long as the command that asked for it is connected.
	put := newMountHost(cfg.Mounts, known)
	offers := newPairHost(n)
	go func() {
		if err := hostLocal(ctx, casts, shares, put, offers, held); err != nil {
			fmt.Fprintf(os.Stderr, "drop: casts unavailable: %v\n", err)
		}
	}()

	doing.said = func(from node.ID, m convo.Message) {
		fmt.Println(render(nameFor(pinned, from), m))
		cfg.FireMessage(conf.Message{
			From: nameFor(pinned, from), Kind: kindName(m.Kind), Body: m.Body, Path: "/chat",
		})
	}

	policy := proto.Policy{
		Mounts:     cfg.Mounts,
		Archetypes: known,
		Allow:      accepting(pinned, false),
		Who:        whoIs(pinned),
		Refused:    noting(pinned),
		Asked:      taking(),
		Met:        meeting(cfg.Mounts, pinned, doing.changed),
	}

	// The address book is re-read before answering anybody, because `drop peer pair` is a separate
	// process: without this, a device paired while this was running stays a stranger to it.
	// Every connection that arrives is kept, and whatever is queued for whoever opened it goes
	// down the same pipe. A device nothing can dial is still a device that dials, and until now
	// its queue only ever emptied in one direction.
	answer := map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = pinned.Refresh()

			// Which path this session was for, so an ephemeral mount learns when the transfer it
			// was put up for is over. Nothing is asked of a caller that was turned away.
			asked, watched := "", policy
			watched.Allow = func(from node.ID, open proto.Opening) (bool, string) {
				allowed, why := policy.Allow(from, open)
				if allowed {
					asked = open.Path
				}
				return allowed, why
			}

			if err := proto.Handle(ctx, s, from, watched); err != nil {
				fmt.Fprintf(os.Stderr, "drop: %v\n", err)
			}
			shares.finished(asked)
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = pinned.Refresh()
			_ = proto.AnswerHello(s, from, func(badge proto.Badged) proto.Hello {
				return greeting(pinned, cfg.Mounts, known, from, badge)
			})
		},
		// Pairing is answered by whoever holds the address, which is this. A separate `drop peer pair`
		// process on this machine asks for a code to be shown; it cannot answer for the node.
		node.ALPNPair: func(from node.ID, s *iroh.Stream) {
			defer s.Close()

			code, _ := offers.asking()
			if code == "" {
				return
			}

			p, err := proto.AnswerPairing(s, n.ID(), from, node.DisplayName(), written(discovery.LocalAddrs(n)))
			if err != nil {
				return
			}
			if !hmac.Equal(p.Proof, codeProof(code, from, n.ID())) {
				fmt.Fprintf(os.Stderr, "drop: %s tried to pair without the code\n", node.Brief(from))
				return
			}
			offers.answered(p)
		},
	}

	// Whatever a device opens on a connection we made is answered the same way as one it made:
	// which side dialled is a fact about the network, not about who may ask what.
	held.Serving(ctx, func(from node.ID, alpn string, s *iroh.Stream) {
		if handle, ok := answer[alpn]; ok {
			handle(from, s)
		}
	})

	pushing := func(from node.ID) {
		// Somebody just opened a connection to us. Whatever is waiting for them can go now, over
		// the connection they are holding, rather than waiting for a dial that may never work.
		_ = pinned.Refresh()

		entry, known := pinned.ByID(from)
		if !known || !entry.Paired() {
			return
		}
		pushTo(ctx, onlyHeld{held: held}, entry, cfg.Mounts, pinned)
	}

	go serveLoopKeeping(ctx, n, answer, held, pushing)

	describe(cfg, known, n, skipped)

	report := time.NewTicker(time.Minute)
	defer report.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nstopping")
			return nil
		case <-report.C:
			if quiet {
				continue
			}
			fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), n.Addr())
		}
	}
}

// describe prints what this node is serving, because a namespace nobody can see is one nobody uses.
func describe(cfg *conf.Config, known *arch.Registry, n *node.Node, skipped []made.Skipped) {
	fmt.Printf("%s  %s\n", node.DisplayName(), n.ID())
	if cfg.Path != "" {
		fmt.Printf("config %s\n", cfg.Path)
	} else {
		fmt.Printf("no config; serving the defaults\n")
	}

	fmt.Println()
	mounts := cfg.Mounts.All()
	kind := widest(6, kinds(mounts))
	for _, m := range mounts {
		fmt.Printf("  %-24s %-*s %-8s %s%s\n", m.Path, kind, kindOf(m.Archetype), m.Source, detail(known, m), sharedAs(m))
	}
	shadowed(skipped)

	// Whether a device that has moved can still be found. There is no way to tell from the outside
	// and it decides whether this works at all once a laptop leaves the building.
	fmt.Println()
	if node.Rendezvous() {
		fmt.Println("  findable from other networks")
	} else {
		fmt.Println("  local networks only  (drop.rendezvous = true to be findable elsewhere)")
	}

	// A second drop on this machine already has the port this identity is reached at. This one can
	// still ask questions, but nothing dialling the identity arrives here — it arrives there, which
	// looks from the outside like this node answering with whatever that one happens to be running.
	if !n.Own() {
		fmt.Println()
		fmt.Println("  ✗ another drop on this machine holds this identity's port")
		fmt.Println("    nothing that dials this device will reach this process.")
		fmt.Println("    stop the other one first:  pkill drop")
	}

	fmt.Println("\nready; ctrl-c to stop")
}

// detail is one namespace in a column: where it points, what it runs. What it says comes from the
// archetype, which is the only thing that knows what its own settings mean.
func detail(known *arch.Registry, m ns.Mount) string {
	answers, ok := known.Lookup(m.Archetype, m.Version)
	if !ok {
		return ""
	}
	return answers.Note(m.Config).Detail
}

// kindOf is what a mount's type is called in a listing. A path with no archetype is a branch: it
// holds others and serves nothing itself.
func kindOf(archetype string) string {
	if archetype == "" {
		return "branch"
	}
	return archetype
}

// backlog keeps trying whatever is still waiting to be said to anybody.
//
// A message to a device that was off is kept rather than lost, and the thing that notices it coming
// back has to be the thing that is always running. Until this, a backlog only moved while somebody
// had a chat window open, which is the one moment they do not need it to.
func backlog(ctx context.Context, pinned *book.Book, held *dial.Kept, mounts *ns.Table) {
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		// Re-read first: a device paired since this started has a conversation too.
		_ = pinned.Refresh()

		for _, entry := range pinned.Paired() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// A connection first, whether or not there is anything to send. This device may be
			// one nothing can dial, and then the connection it opens is the only way anybody has
			// of reaching it — including to hand it what they have been holding.
			if err := held.Reach(ctx, entry, node.ALPNSession); err != nil {
				trace(fmt.Sprintf("reaching %s: %v", entry.Name, err))
			}

			pushTo(ctx, kept{held: held}, entry, mounts, pinned)
		}
	}
}
