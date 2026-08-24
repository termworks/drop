package cmd

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
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
	tickets "github.com/bresilla/drop/src/pkg/ticket"
)

func newPairCmd() *cobra.Command {
	var (
		as     string
		showQR bool
		code   string
		wait   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "pair [ticket]",
		Short: "Link a device to this one, once and for good",
		Long: "Run `drop pair` on one device to get a ticket, then `drop pair <ticket>` on the other.\n" +
			"The two derive a shared secret and can reach each other from then on.\n\n" +
			"A ticket is this node's address and a one-time code. The address is what iroh dials;\n" +
			"the code is what proves the far end was actually invited.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return joinPairing(cmd.Context(), args[0], as, wait)
			}
			return offerPairing(cmd.Context(), as, code, wait, showQR)
		},
	}

	cmd.Flags().StringVar(&as, "as", "", "the local name to file the other device under")
	cmd.Flags().StringVar(&code, "code", "", "use this pairing code instead of a generated one")
	cmd.Flags().BoolVar(&showQR, "qr", false, "draw the ticket as a code a phone can read")
	cmd.Flags().DurationVarP(&wait, "wait", "w", 5*time.Minute, "how long to keep pairing open")

	return cmd
}

// ticketFor is what one device shows and the other types.
//
// It carries where as well as who: an id alone is not dialable until something has resolved
// it, and on a network with no mDNS and no relay there is nothing to do that.
// MaxTicketAddrs caps how many addresses an invitation carries.
//
// Every one of them is twenty characters somebody may have to type, and it is the length of
// the ticket that decides how big its QR code comes out — four addresses makes one too large
// to draw in an ordinary terminal window. The ones left out are not lost: this wire and the
// rendezvous both find a device that moved.
const MaxTicketAddrs = 2

func ticketFor(id node.ID, code string, addrs []netip.AddrPort) string {
	written := make([]string, 0, MaxTicketAddrs)
	for _, a := range likeliest(addrs) {
		if len(written) == MaxTicketAddrs {
			break
		}
		written = append(written, a.String())
	}

	ticket := id.String() + "#" + code
	if len(written) > 0 {
		ticket += "#" + strings.Join(written, ",")
	}
	return ticket
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

func readTicket(text string) (node.ID, string, []netip.AddrPort, error) {
	parts := strings.Split(strings.TrimSpace(text), "#")
	if len(parts) < 2 {
		return node.ID{}, "", nil, fmt.Errorf("that is not a ticket: it should look like <address>#<code>")
	}

	id, err := node.ParseID(parts[0])
	if err != nil {
		return node.ID{}, "", nil, fmt.Errorf("the address in that ticket is not readable: %w", err)
	}

	var addrs []netip.AddrPort
	if len(parts) > 2 && parts[2] != "" {
		for _, written := range strings.Split(parts[2], ",") {
			ap, err := netip.ParseAddrPort(written)
			if err != nil {
				continue
			}
			addrs = append(addrs, ap)
		}
	}
	return id, parts[1], addrs, nil
}

// codeProof binds an attempt to the code, so a device that was not invited cannot complete one.
func codeProof(code string, initiator, responder node.ID) []byte {
	mac := hmac.New(sha256.New, []byte(code))
	fmt.Fprintf(mac, "drop:pair:proof:v1:%s:%s", initiator, responder)
	return mac.Sum(nil)
}

func offerPairing(parent context.Context, as, code string, wait time.Duration, showQR bool) error {
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

	trace("node.Start")
	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	if _, err := discovery.StartLAN(ctx, n); err != nil {
		fmt.Fprintf(os.Stderr, "drop: mDNS unavailable: %v\n", err)
	}

	invite := ticketFor(n.ID(), code, discovery.LocalAddrs(n))

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
		return record(p, as)
	}
}

func joinPairing(parent context.Context, ticket, as string, wait time.Duration) error {
	trace("start")
	id, code, addrs, err := readTicket(tickets.FromLink(ticket))
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

	conn, s, err := reachAt(ctx, n, lan, book.Entry{Name: node.Brief(id), ID: id}, node.ALPNPair, addrs)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer s.Close()

	p, err := proto.Pair(s, n.ID(), node.DisplayName(), codeProof(code, n.ID(), id), written(discovery.LocalAddrs(n)))
	if err != nil {
		return err
	}
	return record(p, as)
}

// record files a completed pairing in the address book.
func record(p proto.Pairing, as string) error {
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
	if err := b.Save(); err != nil {
		return err
	}

	fmt.Printf("\npaired with %s\n", name)
	fmt.Printf("  %s\n\n", p.Peer)
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
	id, code, addrs, err := readTicket(tickets.FromLink(ticket))
	if err != nil {
		return "", err
	}
	if id == n.ID() {
		return "", fmt.Errorf("that is this device's own ticket")
	}

	conn, s, err := dial.At(ctx, n, lan, nil, book.Entry{Name: node.Brief(id), ID: id}, node.ALPNPair, addrs)
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

	return name, pinned.Save()
}
