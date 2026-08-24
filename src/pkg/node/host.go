package node

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strconv"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

// What drop speaks. ALPN is negotiated per connection in iroh, so each protocol is its own ALPN
// rather than a header inside a shared one.
const (
	ALPNSession = "drop/session/1"
	ALPNHello   = "drop/hello/1"
	ALPNPair    = "drop/pair/1"
)

// ALPNs is every protocol this node answers.
var ALPNs = []string{ALPNSession, ALPNHello, ALPNPair}

// Node is this machine on the drop network.
type Node struct {
	Endpoint *iroh.Endpoint

	// borrowed is true when this node could not take the port its identity is reached on, because
	// another process on this machine already has it. That process is the node as far as anybody
	// dialling is concerned; this one can still ask questions, but nobody can reach it.
	borrowed bool
}

// Own reports whether this process holds the address its identity is reached on.
func (n *Node) Own() bool { return !n.borrowed }

// Start brings up the endpoint under this node's persisted identity.
func Start(ctx context.Context) (*Node, error) {
	sk, err := Identity()
	if err != nil {
		return nil, err
	}

	// A fixed port, so the address a peer wrote down at pairing still reaches this device on
	// the next run. With an ephemeral port every restart moves the node and everything that
	// remembered it is pointing at nothing.
	opts := []iroh.Option{
		iroh.WithBindAddr(netip.AddrPortFrom(netip.IPv4Unspecified(), Port())),
		iroh.WithSecretKey(sk),
		iroh.WithALPNs(ALPNs...),
	}

	// Relays are what carry a connection when neither side can be dialled directly, and the
	// address published for a rendezvous is a relay address. Off otherwise: a relay is a
	// third party, and traffic should not start crossing one because a default said so.
	if Rendezvous() {
		opts = append(opts, iroh.WithRelayMode(relayMode()), iroh.WithNetReport())
	}

	borrowed := false

	ep, err := iroh.Bind(ctx, opts...)
	if err != nil && Port() != 0 {
		// The preferred port is taken. An address others wrote down will not reach this node
		// until it is free again, but refusing to start would be worse: everything that does not
		// depend on a remembered address still works.
		borrowed = true
		opts[0] = iroh.WithBindAddr(netip.AddrPortFrom(netip.IPv4Unspecified(), 0))
		ep, err = iroh.Bind(ctx, opts...)
	}
	if err != nil {
		return nil, fmt.Errorf("starting the endpoint: %w", err)
	}
	return &Node{Endpoint: ep, borrowed: borrowed}, nil
}

// ID is this node's address.
func (n *Node) ID() ID {
	return n.Endpoint.ID()
}

// Addr is where this node currently is, as it would be handed to a peer.
func (n *Node) Addr() netaddr.EndpointAddr {
	return n.Endpoint.Addr()
}

// Dial opens a connection to a peer for one protocol.
func (n *Node) Dial(ctx context.Context, at netaddr.EndpointAddr, alpn string) (*iroh.Conn, error) {
	conn, err := n.Endpoint.Connect(ctx, at, alpn)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", Brief(at.ID), err)
	}
	return conn, nil
}

// Accept waits for the next incoming connection.
func (n *Node) Accept(ctx context.Context) (*iroh.Conn, error) {
	return n.Endpoint.Accept(ctx)
}

func (n *Node) Close() error {
	return n.Endpoint.Shutdown(context.Background())
}

// AddrFor builds an address for a peer from its id and whatever is known about where it is.
func AddrFor(id ID, addrs ...netip.AddrPort) netaddr.EndpointAddr {
	at := netaddr.NewEndpointAddr(id)
	for _, ap := range addrs {
		at = at.WithIP(ap)
	}
	return at
}

// DefaultPort is where a drop node listens unless told otherwise.
// Not 51820: that is WireGuard's, and a machine running one would refuse to start drop.
const DefaultPort = 47777

// Port is the UDP port this node binds, from $DROP_PORT or the default.
//
// Zero is allowed and means "pick any", which is right for a one-off command that nobody has
// written an address down for.
func Port() uint16 {
	written := os.Getenv("DROP_PORT")
	if written == "" {
		// A profile listens somewhere of its own, or two of them could not be up at once.
		return profilePort()
	}

	chosen, err := strconv.ParseUint(written, 10, 16)
	if err != nil {
		return profilePort()
	}
	return uint16(chosen)
}

// relayMode is the configured relays, or the defaults when the config named none.
func relayMode() relay.Mode {
	configured := configuredRelays()
	if len(configured) == 0 {
		return relay.ModeDefault()
	}

	urls := make([]netaddr.RelayURL, 0, len(configured))
	for _, raw := range configured {
		u, err := netaddr.ParseRelayURL(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "drop: ignoring relay %q: %v\n", raw, err)
			continue
		}
		urls = append(urls, u)
	}
	if len(urls) == 0 {
		return relay.ModeDefault()
	}
	return relay.ModeCustom(relay.MapFromURLs(urls...))
}
