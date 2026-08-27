// Package link is a place to hand somebody a URL.
//
// It travels the way a message travels, because it is one: what makes a link a link is what the
// far end does when it arrives, which is either to write it down or to open it.
package link

import (
	"context"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/arch/chat"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
)

// Config is what a link namespace was told: what to hand a URL to.
type Config struct {
	// Action is the command a link is given to; empty means it is only recorded.
	Action string
}

// Into is what the process running a link namespace hands it.
type Into struct {
	// Store puts one arriving link away. Returning an error means it was not stored, and the
	// sender will send it again.
	Store func(from node.ID, m convo.Message) error
}

// Link serves links.
type Link struct {
	into Into
}

func New(into Into) *Link { return &Link{into: into} }

func (l *Link) Name() string { return "link" }
func (l *Link) Version() int { return 1 }

// Read takes what a link is handed to.
func (l *Link) Read(d arch.Declared) (arch.Config, error) {
	action, _ := d.String("action")
	return Config{Action: action}, nil
}

func (l *Link) Note(c arch.Config) arch.Note {
	cfg, _ := c.(Config)

	detail := "recorded, not opened"
	if cfg.Action != "" {
		detail = cfg.Action
	}
	return arch.Note{
		Writable: true,
		Detail:   detail,
		About:    "open a link over there",
		Glyph:    "◈",
	}
}

// Serve takes a batch of links.
func (l *Link) Serve(ctx context.Context, at arch.Session) error {
	return chat.Take(at.Conn, at.From, l.into.Store)
}
