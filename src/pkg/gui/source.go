//go:build js || android

// Package gui is drop on a screen: the same interface on a desktop, a phone, and in a browser.
//
// One set of screens for all three, because three sets is three things to design, style and fix.
// What differs between them is not what a person sees but how it reaches a device — which is what
// Source is for.
package gui

import "io"

// Identity is the device this interface is acting as.
type Identity struct {
	Name  string   `json:"name"`
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

// Peer is a device drop knows about.
type Peer struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Paired bool   `json:"paired"`
	Unread int    `json:"unread"`
}

// Space is one path a device shares with us.
type Space struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Writable bool   `json:"writable"`
}

// Message is one entry in a conversation.
type Message struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Mine  bool   `json:"mine"`
	Body  string `json:"body"`
	Extra string `json:"extra"`
	At    int64  `json:"at"`
}

// Source is how the interface reaches devices.
//
// On a desktop it is a node of its own and dials directly. In a browser or on a phone it cannot be
// — neither can open a QUIC connection — so it asks a machine that is one, over the same HTTP the
// page used to. The screens above this do not know which they have.
type Source interface {
	Self() (Identity, error)
	Peers() ([]Peer, error)
	Spaces(peer string) ([]Space, error)
	Log(peer string) ([]Message, error)
	Say(peer, body string, asLink bool) error

	// Offer opens this device to a pairing. The ticket comes back at once, because it is what has
	// to be on screen; With reports who answered, once anyone has.
	Offer() (ticket string, err error)
	Pairing() (ticket, with string, err error)
	Unpair() error

	// Watch reads a live path until the reader is closed. Writes land on the interface's goroutine
	// through a screen that takes its own lock.
	Watch(peer, path string, into io.Writer, resize func(cols, rows int), done <-chan struct{}) error
}
