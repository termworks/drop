// Package mobile is drop as an Android library.
//
// gomobile binds only a narrow set of types, so everything crossing into Kotlin is a string, an
// int64, a bool, or an interface declared here. Lists cross as one entry per line.
package mobile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

// Events is what the app implements to hear from the node.
type Events interface {
	OnStatus(text string)
	OnAddress(text string)
}

// Node is a running drop node.
type Node struct {
	mu   sync.Mutex
	stop context.CancelFunc
	host *node.Node
	id   string
	name string
}

// Start brings a node up with its state under the directories Android gave the app.
//
// The directories are exported through the environment because that is where every store in drop
// looks for them.
func Start(configDir, dataDir, name string, events Events) (*Node, error) {
	if configDir == "" || dataDir == "" {
		return nil, fmt.Errorf("a node needs somewhere to keep its config and its data")
	}
	for _, dir := range []string{configDir, dataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	os.Setenv("XDG_CONFIG_HOME", configDir)
	os.Setenv("XDG_DATA_HOME", dataDir)
	os.Setenv("HOME", filepath.Dir(configDir))
	if name != "" {
		os.Setenv("DROP_NAME", name)
	}

	ctx, stop := context.WithCancel(context.Background())

	host, err := node.Start(ctx)
	if err != nil {
		stop()
		return nil, fmt.Errorf("starting the node: %w", err)
	}

	n := &Node{stop: stop, host: host, name: node.DisplayName()}
	if id, err := node.LocalID(); err == nil {
		n.id = id.String()
	}

	say(events, "this device is "+node.Brief(host.ID()))

	go func() {
		if err := node.Findable(ctx, host); err != nil {
			say(events, "not findable from other networks: "+err.Error())
			return
		}
		tell(events, host.Addr().String())
	}()

	return n, nil
}

// ID is this device's endpoint id.
func (n *Node) ID() string { return n.id }

// Name is what this device calls itself.
func (n *Node) Name() string { return n.name }

// Brief is the short form of the id, for a screen with no room for the whole of it.
func (n *Node) Brief() string {
	if n.host == nil {
		return ""
	}
	return node.Brief(n.host.ID())
}

// Peers is every machine in the address book, one per line: name, tab, short id.
func (n *Node) Peers() string {
	pinned, err := book.Load()
	if err != nil {
		return ""
	}

	var out strings.Builder
	for _, entry := range pinned.All() {
		fmt.Fprintf(&out, "%s\t%s\n", entry.Name, node.Brief(entry.ID))
	}
	return out.String()
}

// Stop takes the node down.
func (n *Node) Stop() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.stop != nil {
		n.stop()
		n.stop = nil
	}
}

func say(events Events, text string) {
	if events != nil {
		events.OnStatus(text)
	}
}

func tell(events Events, text string) {
	if events != nil {
		events.OnAddress(text)
	}
}
