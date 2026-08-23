package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"net/netip"
	"strings"
	"sync"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	tickets "github.com/bresilla/drop/src/pkg/ticket"
)

// ears is the one accept loop this phone gets, and what it is currently willing to answer.
//
// One loop, because two racing for the same endpoint means whichever loses hangs up on a connection
// it does not know — a pairing refused for no reason anybody could see.
type ears struct {
	node   *node.Node
	pinned *book.Book
	mounts *ns.Table

	mu     sync.Mutex
	code   string
	ticket string
	with   string
}

func listenTo(ctx context.Context, n *node.Node, pinned *book.Book, mounts *ns.Table) *ears {
	e := &ears{node: n, pinned: pinned, mounts: mounts}

	go func() {
		for {
			conn, err := n.Accept(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			go e.answer(ctx, conn)
		}
	}()

	return e
}

func (e *ears) answer(ctx context.Context, conn *iroh.Conn) {
	defer conn.Close()

	from := conn.RemoteID()
	alpn := conn.ALPN()

	for {
		s, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}

		go func(s *iroh.Stream) {
			defer s.Close()

			switch alpn {
			case node.ALPNPair:
				e.pairWith(from, s)
			case node.ALPNHello:
				_ = proto.AnswerHello(s, proto.Hello{
					Name:    node.DisplayName(),
					Version: "phone",
					Serves:  proto.Describe(e.mounts, e.who(from)),
				})
			case node.ALPNSession:
				_ = proto.Handle(s, from, proto.Policy{
					Mounts:  e.mounts,
					Dir:     inbox(),
					Allow:   func(node.ID, proto.Open) (bool, string) { return true, "" },
					Who:     e.who,
					Message: e.keep,
				})
			}
		}(s)
	}
}

// offer opens this phone to a pairing and hands back the ticket to show.
func (e *ears) offer() (string, error) {
	code, err := proto.NewCode()
	if err != nil {
		return "", err
	}

	written := make([]string, 0, 2)
	for _, at := range discovery.LocalAddrs(e.node) {
		if len(written) == 2 {
			break
		}
		written = append(written, at.String())
	}

	ticket := e.node.ID().String() + "#" + code
	if len(written) > 0 {
		ticket += "#" + strings.Join(written, ",")
	}

	e.mu.Lock()
	e.code, e.ticket, e.with = code, ticket, ""
	e.mu.Unlock()

	return ticket, nil
}

func (e *ears) pairingState() (string, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.ticket, e.with, nil
}

func (e *ears) stopPairing() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.code, e.ticket, e.with = "", "", ""
	return nil
}

// pairWith answers somebody who read this phone's code.
func (e *ears) pairWith(from node.ID, s *iroh.Stream) {
	e.mu.Lock()
	code := e.code
	e.mu.Unlock()

	if code == "" {
		return // nothing is being offered
	}

	written := make([]string, 0, 2)
	for _, at := range discovery.LocalAddrs(e.node) {
		written = append(written, at.String())
	}

	p, err := proto.AnswerPairing(s, e.node.ID(), node.DisplayName(), written)
	if err != nil {
		return
	}
	// The far end has to prove it was given the code, not merely the address.
	if !hmac.Equal(p.Proof, proofOf(code, from, e.node.ID())) {
		return
	}

	name := p.Name
	if name == "" {
		name = node.Brief(from)
	}
	e.pinned.Pair(name, from, p.Secret, p.Addrs...)
	if err := e.pinned.Save(); err != nil {
		return
	}

	e.mu.Lock()
	e.with, e.code = name, ""
	e.mu.Unlock()
}

// joinFrom pairs with whoever is showing a ticket.
func joinFrom(ctx context.Context, n *node.Node, lan *discovery.LAN, ticket string) (string, error) {
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

	written := make([]string, 0, 2)
	for _, at := range discovery.LocalAddrs(n) {
		written = append(written, at.String())
	}

	p, err := proto.Pair(s, n.ID(), node.DisplayName(), proofOf(code, n.ID(), id), written)
	if err != nil {
		return "", err
	}

	name := p.Name
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

// proofOf binds an attempt to the code, so a device that was not invited cannot complete one. The
// same computation the command line does, because both ends have to agree on it.
func proofOf(code string, initiator, responder node.ID) []byte {
	mac := hmac.New(sha256.New, []byte(code))
	fmt.Fprintf(mac, "drop:pair:proof:v1:%s:%s", initiator, responder)
	return mac.Sum(nil)
}

// readTicket takes an invitation apart.
func readTicket(text string) (node.ID, string, []netip.AddrPort, error) {
	parts := strings.Split(strings.TrimSpace(text), "#")
	if len(parts) < 2 {
		return node.ID{}, "", nil, fmt.Errorf("that does not look like a ticket")
	}

	raw, err := key.ParseEndpointID(parts[0])
	if err != nil {
		return node.ID{}, "", nil, fmt.Errorf("the identity in that ticket is not readable: %w", err)
	}

	var addrs []netip.AddrPort
	if len(parts) > 2 {
		for _, at := range strings.Split(parts[2], ",") {
			if parsed, err := netip.ParseAddrPort(at); err == nil {
				addrs = append(addrs, parsed)
			}
		}
	}
	return raw, parts[1], addrs, nil
}

func (e *ears) who(from node.ID) ns.Caller {
	who := ns.Caller{ID: from.String()}

	if entry, ok := e.pinned.ByID(from); ok {
		who.Name, who.Paired = entry.Name, entry.Paired()
	}
	return who
}

func (e *ears) keep(from node.ID, m convo.Message) error {
	store, err := convo.Open(from)
	if err != nil {
		return err
	}
	_, err = store.Add(m)
	return err
}
