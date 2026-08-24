package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/bresilla/drop/src/pkg/asciicast"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// A cast goes through the node that is already running, rather than standing up a second one.
//
// Two processes cannot listen on one address, so a cast that starts its own endpoint while `drop
// serve` holds the port lands on a different one — and a watcher dialling the address in its
// address book reaches the daemon, which knows nothing about the cast. Feeding the daemon over a
// local socket keeps one node, one listener, and one address that always means the same thing.

// castHost is the terminal being cast through this node, if any.
type castHost struct {
	mu     sync.Mutex
	stage  *cast.Caster
	mounts *ns.Table
}

func newCastHost(mounts *ns.Table) *castHost {
	return &castHost{mounts: mounts}
}

// live is the cast in progress, or nil when nobody is casting.
func (h *castHost) live() *cast.Caster {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.stage
}

// begin puts a cast on the air, and declares the path it is served at.
func (h *castHost) begin(cols, rows uint16) *cast.Caster {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.stage != nil {
		h.stage.Stop()
	}
	h.stage = cast.New(cols, rows)

	_ = h.mounts.Add(ns.Mount{
		Path:   CastPath,
		Kind:   ns.KindTTY,
		Access: ns.Access{AnyPaired: true},
	})
	return h.stage
}

// end takes it off the air, and the path with it.
func (h *castHost) end() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.stage != nil {
		h.stage.Stop()
		h.stage = nil
	}
	h.mounts.Drop(CastPath)
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

// hostCasts listens for a local cast and serves it until it ends.
func hostCasts(ctx context.Context, host *castHost) error {
	path, err := castSocket()
	if err != nil {
		return err
	}

	// A socket left behind by a process that was killed would otherwise make this address
	// permanently unusable.
	_ = os.Remove(path)

	listening, err := net.Listen("unix", path)
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = listening.Close()
		_ = os.Remove(path)
	}()

	for {
		conn, err := listening.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go func() {
			defer conn.Close()
			if err := takeCast(ctx, host, conn); err != nil {
				fmt.Fprintf(os.Stderr, "drop: cast: %v\n", err)
			}
		}()
	}
}

// takeCast reads one cast from the socket and puts it on the air for as long as it lasts.
func takeCast(ctx context.Context, host *castHost, from net.Conn) error {
	reader, head, err := asciicast.NewReader(from)
	if err != nil {
		return err
	}

	stage := host.begin(uint16(head.Width), uint16(head.Height))
	defer host.end()

	fmt.Printf("  a terminal is being cast at %s (%dx%d)\n", CastPath, head.Width, head.Height)
	defer fmt.Printf("  the cast at %s ended\n", CastPath)

	return pump(ctx, reader, stage)
}
