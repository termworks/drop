package cmd

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/go-iroh/iroh"
	"golang.org/x/term"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/rendezvous"
	tickets "github.com/bresilla/drop/src/pkg/ticket"
)

func newPairCmd() *cobra.Command {
	var (
		as      string
		code    string
		wait    time.Duration
		machine bool
		at      []string
	)

	cmd := &cobra.Command{
		Use:   "pair [ticket]",
		Short: "Link a machine to this one, once and for good",
		Long: "Run `drop peer pair` on one machine to get a ticket, then `drop peer pair <ticket>` on\n" +
			"the other. The two derive a shared secret and can reach each other from then on.\n\n" +
			"A ticket is this node's address and a one-time code. The address is what iroh dials;\n" +
			"the code is what proves the far end was actually invited.\n\n" +
			"Pairing is with a person: their user key is learnt, and machines they sign later work\n" +
			"without pairing again. --machine pairs with this machine and no other, which is what a\n" +
			"build server wants, or a deliberate refusal to trust the rest of somebody's machines.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return joinPairing(cmd.Context(), args[0], as, wait, machine, at)
			}
			return offerPairing(cmd.Context(), as, code, wait, machine)
		},
	}

	cmd.Flags().StringVar(&as, "as", "", "the local name to file the other device under")
	cmd.Flags().StringVar(&code, "code", "", "use this pairing code instead of a generated one")
	cmd.Flags().DurationVarP(&wait, "wait", "w", 5*time.Minute, "how long to keep pairing open")
	cmd.Flags().BoolVar(&machine, "machine", false, "pair with this device alone, not with whoever owns it")
	cmd.Flags().StringSliceVar(&at, "at", nil, "where to reach the other device, when finding it fails (host:port)")

	return cmd
}

// ticketFor is what one device shows and the other types: who, and a code proving they were told.
//
// Who, and nothing else. An address is drop's business, not a person's — this wire, a relay, and a
// rendezvous all know how to turn an identity into somewhere to dial, and any address written into
// a ticket is a guess that goes stale the moment a laptop moves to another network. It also made
// the ticket twice as long to type and its code too big to draw.
func ticketFor(id node.ID, code string) string {
	return id.String() + "#" + code
}

func readTicket(text string) (node.ID, string, error) {
	id, code, found := strings.Cut(strings.TrimSpace(text), "#")
	if !found {
		return node.ID{}, "", fmt.Errorf("that is not a ticket: it should look like <address>#<code>")
	}

	at, err := node.ParseID(id)
	if err != nil {
		return node.ID{}, "", fmt.Errorf("the address in that ticket is not readable: %w", err)
	}
	return at, code, nil
}

// asAddrs reads the addresses --at was given.
//
// A ticket says who and never where, because an address in an invitation is a guess about somebody
// else's network. But finding a device needs something to find it with: mDNS reaches the same wire,
// and a rendezvous only works between devices that have already paired. Two machines meeting for the
// first time across a tunnel have neither, and this is how somebody says where to look.
func asAddrs(written []string) ([]netip.AddrPort, error) {
	var out []netip.AddrPort

	for _, one := range written {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		// A bare host is the ordinary port, because that is what somebody has to hand.
		if !strings.Contains(one, ":") {
			one = fmt.Sprintf("%s:%d", one, node.DefaultPort)
		}

		at, err := netip.ParseAddrPort(one)
		if err != nil {
			return nil, fmt.Errorf("--at %q is not an address: %w", one, err)
		}
		out = append(out, at)
	}
	return out, nil
}

// errNotTheCode is what a device that did not hold the code is told.
var errNotTheCode = errors.New("that is not the code being shown")

// codeProof binds an attempt to the code, so a device that was not invited cannot complete one.
func codeProof(code string, initiator, responder node.ID) []byte {
	mac := hmac.New(sha256.New, []byte(code))
	_, _ = fmt.Fprintf(mac, "drop:pair:proof:v1:%s:%s", initiator, responder)
	return mac.Sum(nil)
}

func offerPairing(parent context.Context, as, code string, wait time.Duration, machine bool) error {
	// A given code makes pairing scriptable: the ticket can be built by the caller rather than
	// scraped out of this output.
	if code == "" {
		generated, err := proto.NewCode()
		if err != nil {
			return err
		}
		code = generated
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	// Through the daemon when one is running: it holds this identity's address, so it is the one
	// anybody dialling the ticket will reach, and only it can answer them.
	if err := offerThroughDaemon(ctx, as, code, wait, machine); err == nil {
		return nil
	} else if !errors.Is(err, errNoDaemon) {
		return err
	}

	trace("node.Start")
	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = n.Close() }()

	if _, err := discovery.StartLAN(ctx, n); err != nil {
		fmt.Fprintf(os.Stderr, "drop: mDNS unavailable: %v\n", err)
	}

	// Findable by whoever holds the ticket, for as long as it is being offered. mDNS reaches the
	// same wire and nothing else, and there is no shared secret yet for a rendezvous to use.
	if err := node.Findable(ctx, n); err != nil {
		fmt.Fprintf(os.Stderr, "drop: cannot publish where this device is: %v\n", err)
	}

	invite := ticketFor(n.ID(), code)

	showTicket(invite, wait)

	// One pairing per code. The first that proves it holds the code is written down, and only then
	// answered, so whatever it opens straight afterwards is met by somebody who knows it.
	type filedAs struct {
		p    proto.Pairing
		name string
	}
	var once sync.Mutex
	taken := false
	paired := make(chan filedAs, 1)
	go serveLoop(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNPair: func(from node.ID, s *iroh.Stream) {
			defer func() { _ = s.Close() }()

			_, _ = proto.AnswerPairing(s, n.ID(), from, node.DisplayName(), written(discovery.LocalAddrs(n)), func(p proto.Pairing) error {
				// The far end has to prove it was given the code, not merely the address.
				if !hmac.Equal(p.Proof, codeProof(code, from, n.ID())) {
					fmt.Fprintf(os.Stderr, "drop: %s tried to pair without the code\n", node.Brief(from))
					return errNotTheCode
				}

				once.Lock()
				defer once.Unlock()
				if taken {
					return errors.New("that code has already been used")
				}
				name, err := filed(p, as, machine)
				if err != nil {
					return err
				}
				taken = true
				paired <- filedAs{p: p, name: name}
				return nil
			})
		},
	})

	select {
	case <-ctx.Done():
		return fmt.Errorf("nobody paired within %s", wait)
	case at := <-paired:
		announce(at.p, at.name, machine)
		return nil
	}
}

func joinPairing(parent context.Context, ticket, as string, wait time.Duration, machine bool, at []string) error {
	trace("start")

	ctx, cancel := context.WithTimeout(parent, wait)
	defer cancel()

	// Through the daemon when one is running: it is the node the other device will reach from now
	// on, so it is the one whose address the pairing has to carry.
	name, id, called, err := joinThroughDaemon(ctx, ticket, as, machine, at)
	if err == nil {
		fmt.Printf("\npaired with %s\n  %s\n", name, id)
		if called != "" && !machine {
			fmt.Printf("  a machine of theirs, called %q\n", called)
		}
		fmt.Printf("\neither device can now reach the other by name.\n")
		return nil
	}
	if !errors.Is(err, errNoDaemon) {
		return err
	}

	trace("node.Start")
	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = n.Close() }()

	trace("node started; StartLAN")
	lan, err := discovery.StartLAN(ctx, n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: mDNS unavailable: %v\n", err)
	}

	trace("LAN up; reaching")

	p, name, err := join(ctx, n, lan, ticket, as, machine, at)
	if err != nil {
		return err
	}
	announce(p, name, machine)

	return nil
}

// filed writes a completed pairing into the address book and says what it ended up called.
//
// The one place a pairing is written down, whichever side made it and whichever interface asked.
// Machine means what it says: the device key is kept and the user key is not, so the rest of that
// person's machines stay strangers however many badges they sign.
func filed(p proto.Pairing, as string, machine bool) (string, error) {
	name := as
	if name == "" {
		name = p.Name
	}
	if name == "" {
		name = node.Brief(p.Peer)
	}

	b, err := book.Load()
	if err != nil {
		return "", err
	}
	nextUser := ""
	if !machine {
		nextUser = p.User
	}

	err = b.Change(func() (bool, error) {
		// Refuse to reassign a name held by another machine.
		if held, taken := b.Lookup(name); taken && held.ID != p.Peer {
			return false, fmt.Errorf("%q is already %s here; pair with --as to choose another name", name, node.Brief(held.ID))
		}
		for _, held := range b.All() {
			if held.Person == name && held.User != nextUser && held.ID != p.Peer {
				return false, fmt.Errorf("%q already names a person here; pair with --as to choose another name", name)
			}
			if held.ID == p.Peer && held.Name != name {
				return false, fmt.Errorf("%s is already filed as %q; forget %q before pairing it as %q",
					node.Brief(p.Peer), held.Name, held.Name, name)
			}
		}

		held, replacing := b.Lookup(name)
		keepTrust := replacing && held.ID == p.Peer && held.Trusted && held.User == nextUser

		b.Pair(name, p.Peer, p.Secret, p.Addrs...)
		if !machine {
			b.Belongs(name, p.User)
		}
		if keepTrust {
			b.Trust(name, true)
		}
		return true, nil
	})
	return name, err
}

// announce says who was paired with, for the interfaces that print rather than draw.
func announce(p proto.Pairing, name string, machine bool) {
	fmt.Printf("\npaired with %s\n", name)
	fmt.Printf("  %s\n", p.Peer)
	switch {
	case machine && p.User != "":
		fmt.Printf("  this machine alone; the rest of theirs stay strangers\n")
	case p.User != "":
		fmt.Printf("  a machine of theirs, called %q\n", p.Machine)
	}
	fmt.Println()
	fmt.Printf("either device can now reach the other by name.\n")
}

// record files a pairing and says so, which is what the offering side does when it completes.
func record(p proto.Pairing, as string, machine bool) error {
	name, err := filed(p, as, machine)
	if err != nil {
		return err
	}
	announce(p, name, machine)

	return nil
}

// trace reports progress through pairing while it is being brought up on a new transport.
func trace(step string) {
	if os.Getenv("DROP_TRACE") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[trace] %s\n", step)
}

// written turns addresses into the form the wire and the address book carry.
func written(addrs []netip.AddrPort) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}

// joinWith pairs with whoever is showing a ticket, using a node that is already running.
//
// The command builds its own node and tears it down; an interface already has one, and starting a
// second would mean two endpoints on one identity fighting over a port.
// join is the whole of taking somebody's ticket, over a node that is already up.
//
// One function, because there are two callers -- the command line and the interface -- and when
// this was two functions they drifted: one learnt how to find a device that is not on the same
// wire and the other did not, so pairing worked from one and failed from the other with an error
// that said nothing about why.
func join(ctx context.Context, n *node.Node, lan *discovery.LAN, ticket, as string, machine bool, at []string) (proto.Pairing, string, error) {
	id, code, err := readTicket(tickets.FromLink(ticket))
	if err != nil {
		return proto.Pairing{}, "", err
	}
	if id == n.ID() {
		return proto.Pairing{}, "", fmt.Errorf("that is this device's own ticket")
	}

	where, err := asAddrs(at)
	if err != nil {
		return proto.Pairing{}, "", err
	}

	// Looked up under its own id: pairing is the one exchange with no shared secret to derive a
	// rendezvous key from, and mDNS reaches only the same wire.
	var openly dial.Finder
	if found, err := rendezvous.Open(); err == nil {
		openly = found
	}

	conn, s, err := dial.At(ctx, n, lan, openly, book.Entry{Name: node.Brief(id), ID: id}, node.ALPNPair, where)
	if err != nil {
		return proto.Pairing{}, "", err
	}
	defer func() { _ = conn.Close() }()
	defer func() { _ = s.Close() }()

	p, err := proto.Pair(s, n.ID(), id, node.DisplayName(), codeProof(code, n.ID(), id), written(discovery.LocalAddrs(n)))
	if err != nil {
		return proto.Pairing{}, "", err
	}

	name, err := filed(p, as, machine)
	if err == nil {
		nudgeMine()
	}
	return p, name, err
}

// offerThroughDaemon asks the running node to show a code, and waits for somebody to take it.
func offerThroughDaemon(ctx context.Context, as, code string, wait time.Duration, machine bool) error {
	said, done, err := offerAtDaemon(ctx, code, as, machine)
	if err != nil {
		return err
	}
	defer done()

	id, err := node.LocalID()
	if err != nil {
		return err
	}
	showTicket(ticketFor(id, code), wait)

	select {
	case <-ctx.Done():
		return fmt.Errorf("nobody paired within %s", wait)
	case line, ok := <-said:
		if !ok {
			return errors.New("the node stopped listening")
		}

		what, rest, _ := strings.Cut(line, " ")
		switch what {
		case "paired":
			name, id, _ := strings.Cut(rest, " ")
			fmt.Printf("\npaired with %s\n  %s\n\neither device can now reach the other by name.\n", name, id)
			return nil
		case "busy":
			return errors.New(rest)
		}
		return fmt.Errorf("the node said %q", line)
	}
}

// showTicket prints an invitation the same way whoever is answering it happens to be arranged.
//
// The code is drawn whenever a person is reading, because the other device is as likely to be a
// phone with a camera as a machine with a keyboard. Piped, it is only the text a script wants.
func showTicket(invite string, wait time.Duration) {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if qrCode, err := tickets.Code(invite); err == nil {
			fmt.Printf("\n%s", tickets.Painted(qrCode))
		} else {
			fmt.Fprintf(os.Stderr, "drop: could not draw a code: %v\n", err)
		}
	}

	fmt.Printf("\n  ticket:  %s\n", invite)
	fmt.Printf("  link:    %s\n\n", tickets.Link(invite))
	fmt.Printf("run this on the other machine, within %s:\n\n  drop peer pair %s\n\nwaiting...\n", wait, invite)
}

// offerAtDaemon asks the running node to show a code, and yields the one line it answers with: who
// paired, or why nobody did. What it hands back closes the connection, which is what takes the code
// back down, so an offer that is abandoned does not leave one live.
func offerAtDaemon(ctx context.Context, code, as string, machine bool) (<-chan string, func(), error) {
	path, err := castSocket()
	if err != nil {
		return nil, nil, errNoDaemon
	}

	conn, err := dialLocal(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, errNoDaemon
	}

	// A dash for a name that was not given, and always a kind, so the line is three fields.
	name := as
	if name == "" {
		name = "-"
	}
	kind := "person"
	if machine {
		kind = "machine"
	}
	if err := writeLocal(conn, "pair %s %s %s\n", code, name, kind); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	said := make(chan string, 1)
	go func() {
		line, err := readLocalLine(bufio.NewReader(conn))
		if err != nil {
			close(said)
			return
		}
		said <- strings.TrimSpace(line)
	}()
	return said, func() { _ = conn.Close() }, nil
}
