package cmd

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
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
	"github.com/bresilla/drop/src/pkg/user"
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
			kind := offerPerson
			if machine {
				kind = offerMachine
			}
			if len(args) == 1 {
				return joinPairing(cmd.Context(), args[0], as, wait, kind, at)
			}
			return offerPairing(cmd.Context(), as, code, wait, kind)
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

// offerKind is what a code being shown is for, which decides what the device taking it becomes.
type offerKind string

const (
	// offerPerson pairs with somebody: their machine, and through its badge, them.
	offerPerson offerKind = "person"
	// offerMachine pairs with that device alone, and none of its owner's other machines.
	offerMachine offerKind = "machine"
	// offerMine adds a machine of this user's own, with a badge this machine signs for it.
	offerMine offerKind = "mine"
	// offerMineKey adds one and hands it the key itself, so it signs for itself from then on.
	offerMineKey offerKind = "minekey"
)

func (k offerKind) mine() bool { return k == offerMine || k == offerMineKey }

// admitted is what a machine showing a code does with a device that proved it holds it. A device
// that came for the other kind of code is refused, so nobody becomes somebody's machine by pairing
// with them; one taking a code for a machine of this user's own is given what makes it one, and is
// filed as this user's from the start.
func admitted(p *proto.Pairing, kind offerKind) (proto.Grant, error) {
	switch {
	case kind.mine() && !p.Wants:
		return proto.Grant{}, errors.New("this code adds a machine of mine: take it with `drop machine add <code>`")
	case !kind.mine() && p.Wants:
		return proto.Grant{}, errors.New("this code pairs with a person rather than adding a machine: take it with `drop person add <code>`")
	case !kind.mine():
		return proto.Grant{}, nil
	case p.User != "" && p.User == myKey():
		// Already this user's, by a key of its own or a badge: nothing to hand it.
		return proto.Grant{}, nil
	}

	if kind == offerMineKey {
		seed, err := user.Export()
		if err != nil {
			return proto.Grant{}, err
		}
		p.User = myKey()
		return proto.Grant{Kind: proto.GrantKey, Body: seed}, nil
	}

	name := p.Name
	if name == "" {
		name = node.Brief(p.Peer)
	}
	badge, sig, err := user.Vouch(p.Peer.String(), name, time.Now())
	if err != nil {
		return proto.Grant{}, fmt.Errorf("signing a badge for %s: %w", name, err)
	}
	packed, err := user.Pack(badge, sig)
	if err != nil {
		return proto.Grant{}, err
	}
	p.User = myKey()
	return proto.Grant{Kind: proto.GrantBadge, Body: packed}, nil
}

// canAdd says, before any code is shown, why this machine could not make another one its user's.
func canAdd(kind offerKind) error {
	switch kind {
	case offerMine:
		if _, quiet := user.Quiet(); quiet || user.Named() || user.CanAssert() {
			return nil
		}
		return errors.New("this machine wears a badge another one signed, so it cannot sign one: run `drop machine add` on a machine that holds your key")
	case offerMineKey:
		_, err := user.Export()
		return err
	}
	return nil
}

// wearGrant makes this machine what the machine showing the code made it: one of its user's, by a
// badge signed for it or by the key itself, worn from this moment.
func wearGrant(g proto.Grant) error {
	switch g.Kind {
	case proto.GrantBadge:
		badge, sig, err := user.Unpack(g.Body, time.Now())
		if err != nil {
			return err
		}
		if err := user.Wear(badge, sig, time.Now()); err != nil {
			return err
		}
	case proto.GrantKey:
		if err := user.Import(g.Body); err != nil {
			return err
		}
	default:
		return errors.New("the machine showing that code sent nothing that makes this one its own")
	}
	return wearBadge()
}

// publishCode puts a code up for finding by itself: whoever types just the code looks it up and gets
// this machine's id, so nobody has to type sixty-four characters of it.
func publishCode(ctx context.Context, code string, id node.ID, kind offerKind) {
	if !node.Rendezvous() {
		return
	}
	if err := rendezvous.PublishCode(ctx, code, id, kindSaid(kind)); err != nil {
		fmt.Fprintf(os.Stderr, "drop: the code cannot be looked up by itself: %v\n", err)
	}
}

// kindSaid is what a code is for, as its record and its link say it: the one thing whoever takes it
// needs to know to take it the right way.
func kindSaid(kind offerKind) string {
	if kind.mine() {
		return string(offerMine)
	}
	return string(offerPerson)
}

// whoShows is the machine a ticket or a bare code names, the code, and what it is for when that is
// said — a link says it, and so does the record a code is looked up by.
func whoShows(ctx context.Context, text string) (node.ID, string, offerKind, error) {
	said := offerKind("")
	switch kind, _ := tickets.Kind(text); {
	case kind == tickets.KindMachine:
		said = offerMine
	case strings.HasPrefix(strings.TrimSpace(text), tickets.Link("")):
		said = offerPerson
	}
	text = strings.TrimSpace(tickets.FromLink(text))
	if strings.Contains(text, "#") {
		id, code, err := readTicket(text)
		return id, code, said, err
	}
	code := rendezvous.NormalCode(text)
	if code == "" {
		return node.ID{}, "", "", errors.New("that is not a code")
	}
	found, err := rendezvous.Open()
	if err != nil {
		return node.ID{}, "", "", err
	}
	look, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id, kind, ok := found.FindCode(look, code)
	if !ok {
		return node.ID{}, "", "", fmt.Errorf("nothing is showing %s: check it, and that the other machine is still waiting", code)
	}
	return id, code, offerKind(kind), nil
}

// codeProof binds an attempt to the code, so a device that was not invited cannot complete one.
func codeProof(code string, initiator, responder node.ID) []byte {
	mac := hmac.New(sha256.New, []byte(code))
	_, _ = fmt.Fprintf(mac, "drop:pair:proof:v1:%s:%s", initiator, responder)
	return mac.Sum(nil)
}

func offerPairing(parent context.Context, as, code string, wait time.Duration, kind offerKind) error {
	// A given code makes pairing scriptable: the ticket can be built by the caller rather than
	// scraped out of this output.
	if code == "" {
		generated, err := proto.NewCode()
		if err != nil {
			return err
		}
		code = generated
	}
	code = rendezvous.NormalCode(code)
	if err := canAdd(kind); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	// Through the daemon when one is running: it holds this identity's address, so it is the one
	// anybody dialling the ticket will reach, and only it can answer them.
	if err := offerThroughDaemon(ctx, as, code, wait, kind); err == nil {
		return nil
	} else if !errors.Is(err, errNoDaemon) {
		return err
	}

	trace("node.Start")
	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	// Cancelled first, so the loop answering on it knows the close is on purpose.
	defer func() { cancel(); _ = n.Close() }()

	if _, err := discovery.StartLAN(ctx, n); err != nil {
		fmt.Fprintf(os.Stderr, "drop: mDNS unavailable: %v\n", err)
	}

	// Findable by whoever holds the ticket, for as long as it is being offered. mDNS reaches the
	// same wire and nothing else, and there is no shared secret yet for a rendezvous to use.
	if err := node.Findable(ctx, n); err != nil {
		fmt.Fprintf(os.Stderr, "drop: cannot publish where this device is: %v\n", err)
	}

	invite := ticketFor(n.ID(), code)
	publishCode(ctx, code, n.ID(), kind)

	showTicket(invite, code, wait, kind)

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

			var done *filedAs
			_, err := proto.AnswerPairing(s, n.ID(), from, node.DisplayName(), written(discovery.LocalAddrs(n)), func(p proto.Pairing) (proto.Grant, error) {
				// The far end has to prove it was given the code, not merely the address.
				if !hmac.Equal(p.Proof, codeProof(code, from, n.ID())) {
					fmt.Fprintf(os.Stderr, "drop: %s tried to pair without the code\n", node.Brief(from))
					return proto.Grant{}, errNotTheCode
				}

				once.Lock()
				defer once.Unlock()
				if taken {
					return proto.Grant{}, errors.New("that code has already been used")
				}
				grant, err := admitted(&p, kind)
				if err != nil {
					return proto.Grant{}, err
				}
				name, err := filed(p, as, kind == offerMachine)
				if err != nil {
					return proto.Grant{}, err
				}
				taken = true
				done = &filedAs{p: p, name: name}
				return grant, nil
			})
			if err != nil || done == nil {
				return
			}

			// Handed on only once the far end has read the answer and hung up: this process exits
			// the moment it hears, and an endpoint closed under an answer still in flight loses it.
			_ = s.Close()
			_ = s.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _ = io.Copy(io.Discard, s)
			paired <- *done
		},
	})

	select {
	case <-ctx.Done():
		return fmt.Errorf("nobody paired within %s", wait)
	case at := <-paired:
		announce(at.name, at.p.Peer.String(), at.p.Machine, kind)
		return nil
	}
}

func joinPairing(parent context.Context, ticket, as string, wait time.Duration, kind offerKind, at []string) error {
	trace("start")

	ctx, cancel := context.WithTimeout(parent, wait)
	defer cancel()

	// Through the daemon when one is running: it is the node the other device will reach from now
	// on, so it is the one whose address the pairing has to carry.
	name, id, called, err := joinThroughDaemon(ctx, ticket, as, kind, at)
	if err == nil {
		joined(name, id, called, kind)
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

	p, name, err := join(ctx, n, lan, ticket, as, kind, at)
	if err != nil {
		return err
	}
	joined(name, p.Peer.String(), p.Machine, kind)

	return nil
}

// filed writes a completed pairing into the address book and says what it ended up called.
//
// The one place a pairing is written down, whichever side made it and whichever interface asked.
// Machine means what it says: the device key is kept and the user key is not, so the rest of that
// person's machines stay strangers however many badges they sign.
func filed(p proto.Pairing, as string, machine bool) (string, error) {
	// Pairing again with something taken out is putting it back, on every machine of this user's.
	if err := user.Restore(p.Peer.String(), time.Now()); err != nil {
		return "", err
	}
	defer nudgeMine()

	b, err := book.Load()
	if err != nil {
		return "", err
	}

	// A device already known keeps the name it is known by, unless another was asked for.
	name := as
	if held, known := b.ByID(p.Peer); name == "" && known {
		name = held.Name
	}
	if name == "" {
		name = p.Name
	}
	if name == "" {
		name = node.Brief(p.Peer)
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

// joined says what taking a code made this machine: one of its user's, or paired with somebody.
func joined(name, id, called string, kind offerKind) {
	if kind == offerAny && sameUserAs(name) {
		kind = offerMine
	}
	if !kind.mine() {
		announce(name, id, called, kind)
		return
	}
	fmt.Printf("\nthis machine is one of yours now, with %s\n  %s\n", name, id)
	fmt.Printf("  your key  %s\n", keyPrint())
	fmt.Printf("\nthe rest of your machines hear about it within a few minutes, and it about them.\n")
}

// sameUserAs reports whether a device in the book belongs to whoever this machine does now.
func sameUserAs(name string) bool {
	pinned, err := book.Load()
	if err != nil {
		return false
	}
	entry, ok := pinned.Lookup(name)
	if !ok || entry.User == "" {
		return false
	}
	pub, err := user.Public()
	return err == nil && entry.User == user.Text(pub)
}

// keyPrint is the fingerprint of the key this machine belongs to, read afresh: taking a code can
// have just changed it.
func keyPrint() string {
	pub, err := user.Public()
	if err != nil {
		return "unreadable"
	}
	return user.Fingerprint(pub)
}

// announce says who was paired with, for the interfaces that print rather than draw.
func announce(name, id, called string, kind offerKind) {
	if kind == offerAny {
		fmt.Printf("\nconnected with %s\n  %s\n", name, id)
		return
	}
	if kind.mine() {
		fmt.Printf("\n%s is one of your machines now\n  %s\n", name, id)
		fmt.Printf("  signed with your key %s\n", keyPrint())
		fmt.Printf("\nthe rest of your machines hear about it within a few minutes, and it about them.\n")
		return
	}
	fmt.Printf("\npaired with %s\n  %s\n", name, id)
	switch {
	case kind == offerMachine:
		fmt.Printf("  this machine alone; the rest of theirs stay strangers\n")
	case called != "":
		fmt.Printf("  a machine of theirs, called %q\n", called)
	}
	fmt.Println()
	fmt.Printf("either device can now reach the other by name.\n")
}

// record files a pairing and says so, which is what the offering side does when it completes, and
// says what it was filed under.
func record(p proto.Pairing, as string, kind offerKind) (string, error) {
	name, err := filed(p, as, kind == offerMachine)
	if err != nil {
		return "", err
	}
	announce(name, p.Peer.String(), p.Machine, kind)

	return name, nil
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

// join is the whole of taking somebody's ticket or code, over a node that is already up.
//
// One function, because there are two callers -- the command line and the interface -- and when
// this was two functions they drifted: one learnt how to find a device that is not on the same
// wire and the other did not, so pairing worked from one and failed from the other with an error
// that said nothing about why.
func join(ctx context.Context, n *node.Node, lan *discovery.LAN, ticket, as string, kind offerKind, at []string) (proto.Pairing, string, error) {
	id, code, said, err := whoShows(ctx, ticket)
	if err != nil {
		return proto.Pairing{}, "", err
	}
	if id == n.ID() {
		return proto.Pairing{}, "", fmt.Errorf("that is this device's own ticket")
	}

	// Taken the way the code says it is meant, when whoever joins left it to the code. A code from a
	// drop that does not say is tried as pairing, and as joining when that is what it turns out to be.
	if kind == offerAny {
		kind = said
		if kind == "" {
			p, name, err := joinTo(ctx, n, lan, id, code, as, offerPerson, at)
			if err != nil && strings.Contains(err.Error(), "adds a machine of mine") {
				return joinTo(ctx, n, lan, id, code, as, offerMine, at)
			}
			return p, name, err
		}
	}
	return joinTo(ctx, n, lan, id, code, as, kind, at)
}

// offerAny is joining with whatever a code is for: pairing with somebody, or becoming one of their
// machines.
const offerAny offerKind = "any"

// joinTo takes the code a machine is showing, one way.
func joinTo(ctx context.Context, n *node.Node, lan *discovery.LAN, id node.ID, code, as string, kind offerKind, at []string) (proto.Pairing, string, error) {
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

	p, err := proto.Pair(s, n.ID(), id, node.DisplayName(), codeProof(code, n.ID(), id), written(discovery.LocalAddrs(n)), kind.mine())
	if err != nil {
		return proto.Pairing{}, "", err
	}

	// Worn before it is filed, so the machine that showed the code is filed as this user's own. One
	// already this user's is handed nothing, and needs nothing.
	already := p.Grant.Kind == proto.GrantNone && p.User != "" && p.User == myKey()
	if kind.mine() && !already {
		if err := wearGrant(p.Grant); err != nil {
			return proto.Pairing{}, "", fmt.Errorf("becoming one of your machines: %w", err)
		}
	}
	name, err := filed(p, as, kind == offerMachine)
	if err == nil {
		nudgeMine()
	}
	return p, name, err
}

// offerThroughDaemon asks the running node to show a code, and waits for somebody to take it.
func offerThroughDaemon(ctx context.Context, as, code string, wait time.Duration, kind offerKind) error {
	said, done, err := offerAtDaemon(ctx, code, as, kind)
	if err != nil {
		return err
	}
	defer done()

	id, err := node.LocalID()
	if err != nil {
		return err
	}
	showTicket(ticketFor(id, code), code, wait, kind)

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
			announce(name, id, "", kind)
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
// phone with a camera as a machine with a keyboard. Piped, it is only the text a script wants. The
// short code is what a person types: it is looked up, so the id never has to be.
func showTicket(invite, code string, wait time.Duration, kind offerKind) {
	// One command takes either kind: the code says which it is.
	link, command := tickets.Link(invite), "drop person add"
	if kind.mine() {
		link, command = tickets.LinkAs(tickets.KindMachine, invite), "drop machine add"
	}

	if term.IsTerminal(int(os.Stdout.Fd())) {
		if qrCode, err := tickets.CodeOf(link); err == nil {
			fmt.Printf("\n%s", tickets.Painted(qrCode))
		} else {
			fmt.Fprintf(os.Stderr, "drop: could not draw a code: %v\n", err)
		}
	}

	fmt.Printf("\n  code:    %s\n", code)
	fmt.Printf("  link:    %s\n", link)
	if kind.mine() {
		// What the new machine is made yours by, so nobody joins one without seeing it.
		fmt.Printf("  key:     %s\n", keySays())
		if _, quiet := user.Quiet(); quiet && !user.Named() {
			fmt.Printf("           `drop me key use ~/.ssh/id_ed25519` makes it your SSH key\n")
		}
	}
	fmt.Println()
	fmt.Printf("on the other machine, within %s, run\n\n  %s %s\n\n", wait, command, code)
	fmt.Printf("or on a phone, tap Add in drop and scan the code above.\n\n")
	fmt.Printf("waiting...\n")
}

// offerAtDaemon asks the running node to show a code, and yields the one line it answers with: who
// paired, or why nobody did. What it hands back closes the connection, which is what takes the code
// back down, so an offer that is abandoned does not leave one live.
func offerAtDaemon(ctx context.Context, code, as string, kind offerKind) (<-chan string, func(), error) {
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
