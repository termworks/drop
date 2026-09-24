// Package mobile is drop as an Android library.
//
// It runs the same node the full-screen interface runs, through the same entry point, so a phone
// is not a second implementation of anything. gomobile binds only strings, numbers, bools, byte
// slices and interfaces declared here, so lists and records cross as JSON.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/bresilla/drop/src/cmd"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/tui"
	"github.com/bresilla/drop/src/pkg/user"
)

// Events is what the app implements to hear from the node.
type Events interface {
	// Changed says something is different and whatever is on screen should be read again.
	Changed()
	// Said is a message that has just landed: who it is from, and what it says.
	Said(from, text string)
	// Paired is a pairing that completed while this device was showing its code.
	Paired(with string)
	// Trouble is something that went wrong without stopping anything.
	Trouble(text string)
	// Moving is how far a transfer has got.
	Moving(name string, done, size int64)
	// Landed is a file that has just arrived: who sent it, what it is called, and where it is now.
	Landed(from, name, at string)
}

// Node is a running drop node.
type Node struct {
	mu     sync.Mutex
	ctx    context.Context
	stop   context.CancelFunc
	back   tui.Backend
	down   func()
	events Events
	offer  context.CancelFunc
	// data and downloads are where what this phone takes up is kept: a note in the one, a folder
	// somebody can see in the other.
	data, downloads string
}

// Start brings a node up with its state under the directories Android gave the app.
//
// The directories are handed over through the environment, because that is where every store in
// drop looks for them. What arrives from other devices lands in downloads, under drop/.
func Start(configDir, dataDir, downloads, name string, events Events) (*Node, error) {
	if configDir == "" || dataDir == "" {
		return nil, fmt.Errorf("a node needs somewhere to keep its config and its data")
	}
	for _, dir := range []string{configDir, dataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	where := map[string]string{
		"XDG_CONFIG_HOME": configDir,
		"XDG_DATA_HOME":   dataDir,
		"HOME":            filepath.Dir(configDir),
	}
	if downloads != "" {
		where["XDG_DOWNLOAD_DIR"] = downloads
	}
	if name != "" {
		where["DROP_NAME"] = name
	}
	for key, value := range where {
		if err := os.Setenv(key, value); err != nil {
			return nil, fmt.Errorf("setting %s: %w", key, err)
		}
	}

	ctx, stop := context.WithCancel(context.Background())
	back, down, err := cmd.Interface(ctx, cmd.Hooks{
		Trouble: func(text string) { trouble(events, text) },
		Said: func(from string, m convo.Message) {
			if events != nil && (m.Kind == convo.KindText || m.Kind == convo.KindLink) {
				events.Said(from, m.Body)
			}
		},
		Landed: func(from, name string, size int64) {
			if events != nil {
				events.Landed(from, name, filepath.Join(downloads, "drop", filepath.Base(name)))
			}
		},
	})
	if err != nil {
		stop()
		return nil, fmt.Errorf("starting the node: %w", err)
	}

	n := &Node{ctx: ctx, stop: stop, back: back, down: down, events: events, data: dataDir, downloads: downloads}
	go n.listen()
	return n, nil
}

// listen passes every arrival on, so a screen shows what has happened rather than what had.
func (n *Node) listen() {
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-n.back.Arrivals():
			changed(n.events)
		}
	}
}

// Stop takes the node down.
func (n *Node) Stop() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.offer != nil {
		n.offer()
		n.offer = nil
	}
	if n.stop != nil {
		n.stop()
		n.down()
		n.stop = nil
	}
}

type self struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	Brief string `json:"brief"`
	User  string `json:"user"`
	// Owner is the user key as a person recognises it, and Signs whether this device holds it: one
	// that does not was vouched for by a machine that does, and wears a badge that runs out Until.
	Owner string `json:"owner"`
	Signs bool   `json:"signs"`
	Until int64  `json:"until"`
}

// Self is this device: what it is called and who it is.
func (n *Node) Self() string {
	me, err := n.back.Self()
	if err != nil {
		return "{}"
	}
	out := self{Name: me.Name, ID: me.ID, Brief: brief(me.ID), User: me.User}
	if pub, err := user.Public(); err == nil {
		out.Owner = user.Fingerprint(pub)
	}
	_, out.Signs = user.Quiet()
	if badge, _, err := user.Mine(time.Now()); err == nil || errors.Is(err, user.ErrStale) {
		out.Until = badge.Until.UnixMilli()
	}
	return encode(out)
}

type machine struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Brief    string `json:"brief"`
	Paired   bool   `json:"paired"`
	Reaching bool   `json:"reaching"`
	Trusted  bool   `json:"trusted"`
}

type person struct {
	Name     string    `json:"name"`
	Me       bool      `json:"me"`
	Anon     bool      `json:"anon"`
	Trusted  bool      `json:"trusted"`
	Machines []machine `json:"machines"`
}

// People is the address book arranged the way the interface arranges it: people, each with the
// machines that are theirs. You first, then everybody else by name, then machines that are nobody's.
func (n *Node) People() (string, error) {
	me, err := n.back.Self()
	if err != nil {
		return "", err
	}
	peers, err := n.back.Peers()
	if err != nil {
		return "", err
	}
	reaching := n.back.Reaching()

	byName := map[string]*person{}
	for _, p := range peers {
		who := userOf(me, p)
		at, ok := byName[who]
		if !ok {
			at = &person{Name: who, Me: who == tui.Me, Anon: who == tui.Anon}
			byName[who] = at
		}
		at.Trusted = at.Trusted || p.Trusted
		at.Machines = append(at.Machines, machine{
			Name:     p.Name,
			ID:       p.ID.String(),
			Brief:    node.Brief(p.ID),
			Paired:   p.Paired(),
			Reaching: reaching[p.Name],
			Trusted:  p.Trusted,
		})
	}

	out := make([]person, 0, len(byName))
	for _, p := range byName {
		sort.Slice(p.Machines, func(i, j int) bool { return p.Machines[i].Name < p.Machines[j].Name })
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return out[i].Name < out[j].Name
	})
	return encode(out), nil
}

// userOf is whose a machine is, the way the interface decides it.
func userOf(me tui.Identity, p book.Entry) string {
	switch {
	case !p.Owned():
		return tui.Anon
	case me.User != "" && p.User == me.User:
		return tui.Me
	case p.Person != "":
		return p.Person
	default:
		return p.Name
	}
}

func rank(p person) int {
	switch {
	case p.Me:
		return 0
	case p.Anon:
		return 2
	default:
		return 1
	}
}

// Trust marks somebody trusted or not, with every machine of theirs.
func (n *Node) Trust(name string, trusted bool) error { return n.back.Trust(name, trusted) }

// Forget drops a pairing, so they arrive as a stranger from then on.
func (n *Node) Forget(name string) error {
	err := n.back.Forget(name)
	changed(n.events)
	return err
}

func entry(name string) (book.Entry, error) { return cmd.Entry(name) }

func encode(v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(body)
}

func brief(id string) string {
	parsed, err := node.ParseID(id)
	if err != nil {
		return id
	}
	return node.Brief(parsed)
}

func changed(events Events) {
	if events != nil {
		events.Changed()
	}
}

func trouble(events Events, text string) {
	if events != nil {
		events.Trouble(text)
	}
}
