package cmd

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/go-iroh/iroh"

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
		showQR  bool
		code    string
		wait    time.Duration
		machine bool
		at      []string
	)

	cmd := &cobra.Command{
		Use:   "pair [ticket]",
		Short: "Link a device to this one, once and for good",
		Long: "Run `drop pair` on one device to get a ticket, then `drop pair <ticket>` on the other.\n" +
			"The two derive a shared secret and can reach each other from then on.\n\n" +
			"A ticket is this node's address and a one-time code. The address is what iroh dials;\n" +
			"the code is what proves the far end was actually invited.\n\n" +
			"Pairing is with a person: their user key is learnt, and machines they sign later work\n" +
			"without pairing again. --machine pairs with this device and no other, which is what a\n" +
			"build server wants, or a deliberate refusal to trust the rest of somebody's machines.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return joinPairing(cmd.Context(), args[0], as, wait, machine, at)
			}
			return offerPairing(cmd.Context(), as, code, wait, showQR, machine)
		},
	}

	cmd.Flags().StringVar(&as, "as", "", "the local name to file the other device under")
	cmd.Flags().StringVar(&code, "code", "", "use this pairing code instead of a generated one")
	cmd.Flags().BoolVar(&showQR, "qr", false, "draw the ticket as a code a phone can read")
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

// likeliest sorts addresses by how likely they are to reach this machine from another one.
//
// An ordinary home or office network first, then anything else. A virtual bridge is put last:
// libvirt and docker hand out 192.168.122.x and 172.17.x on every machine that runs them, so
// the address is real here and means nothing there.
func likeliest(addrs []netip.AddrPort) []netip.AddrPort {
	out := make([]netip.AddrPort, 0, len(addrs))
	for _, at := range addrs {
		// Dropped rather than ranked last: it is the same address on the far machine as on this
		// one, so offering it sends them to themselves. A slot spent on it is a slot wasted.
		if virtual(at.Addr()) {
			continue
		}
		out = append(out, at)
	}

	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

func rank(at netip.AddrPort) int {
	ip := at.Addr()

	switch {
	case ip.IsPrivate():
		return 1
	case ip.IsLoopback() || ip.IsLinkLocalUnicast():
		return 4
	default:
		return 2
	}
}

// virtual spots the ranges a hypervisor or a container runtime hands out on every host.
func virtual(ip netip.Addr) bool {
	if !ip.Is4() {
		return false
	}
	b := ip.As4()

	switch {
	case b[0] == 192 && b[1] == 168 && b[2] == 122: // libvirt
		return true
	case b[0] == 172 && b[1] >= 17 && b[1] <= 31: // docker
		return true
	}
	return false
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

// codeProof binds an attempt to the code, so a device that was not invited cannot complete one.
func codeProof(code string, initiator, responder node.ID) []byte {
	mac := hmac.New(sha256.New, []byte(code))
	fmt.Fprintf(mac, "drop:pair:proof:v1:%s:%s", initiator, responder)
	return mac.Sum(nil)
}

func offerPairing(parent context.Context, as, code string, wait time.Duration, showQR, machine bool) error {
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
	if err := offerThroughDaemon(ctx, as, code, wait, showQR, machine); err == nil {
		return nil
	} else if !errors.Is(err, errNoDaemon) {
		return err
	}

	trace("node.Start")
	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	if _, err := discovery.StartLAN(ctx, n); err != nil {
		fmt.Fprintf(os.Stderr, "drop: mDNS unavailable: %v\n", err)
	}

	// Findable by whoever holds the ticket, for as long as it is being offered. mDNS reaches the
	// same wire and nothing else, and there is no shared secret yet for a rendezvous to use.
	if err := node.Findable(ctx, n); err != nil {
		fmt.Fprintf(os.Stderr, "drop: cannot publish where this device is: %v\n", err)
	}

	invite := ticketFor(n.ID(), code)

	showTicket(invite, wait, showQR)

	paired := make(chan proto.Pairing, 1)
	go serveLoop(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNPair: func(from node.ID, s *iroh.Stream) {
			defer s.Close()

			p, err := proto.AnswerPairing(s, n.ID(), node.DisplayName(), written(discovery.LocalAddrs(n)))
			if err != nil {
				return
			}
			// The far end has to prove it was given the code, not merely the address.
			if !hmac.Equal(p.Proof, codeProof(code, from, n.ID())) {
				fmt.Fprintf(os.Stderr, "drop: %s tried to pair without the code\n", node.Brief(from))
				return
			}
			select {
			case paired <- p:
			default:
			}
		},
	})

	select {
	case <-ctx.Done():
		return fmt.Errorf("nobody paired within %s", wait)
	case p := <-paired:
		return record(p, as, machine)
	}
}

func joinPairing(parent context.Context, ticket, as string, wait time.Duration, machine bool, at []string) error {
	trace("start")
	id, code, err := readTicket(tickets.FromLink(ticket))
	trace("ticket read")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(parent, wait)
	defer cancel()

	trace("node.Start")
	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	trace("node started; StartLAN")
	lan, err := discovery.StartLAN(ctx, n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: mDNS unavailable: %v\n", err)
	}

	trace("LAN up; reaching")
	fmt.Printf("reaching %s...\n", node.Brief(id))

	where, err := asAddrs(at)
	if err != nil {
		return err
	}

	// Looked up under its own id: pairing is the one exchange with no shared secret to derive a
	// rendezvous key from, and mDNS reaches only the same wire.
	var openly dial.Finder
	if found, err := rendezvous.Open(); err == nil {
		openly = found
	}

	conn, s, err := dial.At(ctx, n, lan, openly, book.Entry{Name: node.Brief(id), ID: id}, node.ALPNPair, where)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer s.Close()

	p, err := proto.Pair(s, n.ID(), node.DisplayName(), codeProof(code, n.ID(), id), written(discovery.LocalAddrs(n)))
	if err != nil {
		return err
	}
	return record(p, as, machine)
}

// record files a completed pairing in the address book.
//
// Machine means what it says: the device key is kept and the user key is not, so the rest of that
// person's machines stay strangers however many badges they sign.
func record(p proto.Pairing, as string, machine bool) error {
	name := as
	if name == "" {
		name = p.Name
	}
	if name == "" {
		name = node.Brief(p.Peer)
	}

	b, err := book.Load()
	if err != nil {
		return err
	}
	b.Pair(name, p.Peer, p.Secret, p.Addrs...)
	if !machine {
		b.Belongs(name, p.User)
	}
	if err := b.Save(); err != nil {
		return err
	}

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
func joinWith(ctx context.Context, n *node.Node, lan *discovery.LAN, ticket, as string) (string, error) {
	id, code, err := readTicket(tickets.FromLink(ticket))
	if err != nil {
		return "", err
	}
	if id == n.ID() {
		return "", fmt.Errorf("that is this device's own ticket")
	}

	conn, s, err := dial.At(ctx, n, lan, nil, book.Entry{Name: node.Brief(id), ID: id}, node.ALPNPair, nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	defer s.Close()

	p, err := proto.Pair(s, n.ID(), node.DisplayName(), codeProof(code, n.ID(), id), written(discovery.LocalAddrs(n)))
	if err != nil {
		return "", err
	}

	name := as
	if name == "" {
		name = p.Name
	}
	if name == "" {
		name = node.Brief(id)
	}

	pinned, err := book.Load()
	if err != nil {
		return "", err
	}
	pinned.Pair(name, id, p.Secret, p.Addrs...)
	pinned.Belongs(name, p.User)

	return name, pinned.Save()
}

// offerThroughDaemon asks the running node to show a code, and waits for somebody to take it.
func offerThroughDaemon(ctx context.Context, as, code string, wait time.Duration, showQR, machine bool) error {
	path, err := castSocket()
	if err != nil {
		return errNoDaemon
	}

	conn, err := net.Dial("unix", path)
	if err != nil {
		return errNoDaemon
	}
	defer conn.Close()

	id, err := node.LocalID()
	if err != nil {
		return err
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
	if _, err := fmt.Fprintf(conn, "pair %s %s %s\n", code, name, kind); err != nil {
		return err
	}

	showTicket(ticketFor(id, code), wait, showQR)

	// The daemon answers with one line: who paired, or why nobody did. Closing this connection is
	// what takes the code back down, so a cancelled command does not leave one live.
	said := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			close(said)
			return
		}
		said <- strings.TrimSpace(line)
	}()

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
func showTicket(invite string, wait time.Duration, showQR bool) {
	if showQR {
		if qrCode, err := tickets.Code(invite); err == nil {
			fmt.Printf("\n%s", tickets.Render(qrCode))
		} else {
			fmt.Fprintf(os.Stderr, "drop: could not draw a code: %v\n", err)
		}
	}

	fmt.Printf("\n  ticket:  %s\n", invite)
	fmt.Printf("  link:    %s\n\n", tickets.Link(invite))
	fmt.Printf("run this on the other device, within %s:\n\n  drop pair %s\n\nwaiting...\n", wait, invite)
}
