//go:build js || android

package gui

import (
	"image"
	"image/color"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// level is how deep you have gone: the devices, then what one of them shares, then the thing.
type level int

const (
	atDevices level = iota
	atPaths
	atOpen
)

// App is the whole interface.
//
// One of these is behind a desktop window, a phone, and a browser tab. What differs is the Source
// it was given, which is the only thing that knows how a device is reached.
type App struct {
	from  Source
	theme *material.Theme

	mu      sync.Mutex
	me      Identity
	peers   []Peer
	paths   []Space
	history []Message
	trouble string
	busy    bool

	at     level
	onPeer int
	onPath int

	// Widget state has to outlive a frame, so it is kept rather than made each time.
	list       layout.List
	rows       []widget.Clickable
	back       widget.Clickable
	refresh    widget.Clickable
	compose    widget.Editor
	send       widget.Clickable
	invalidate func()
}

// New builds the interface over a source.
func New(from Source) *App {
	a := &App{
		from:  from,
		theme: newTheme(),
		list:  layout.List{Axis: layout.Vertical},
	}
	a.compose.SingleLine = true
	a.compose.Submit = true
	return a
}

// Start loads what the first screen needs. Called once the window exists, so a redraw can be asked
// for when an answer arrives.
func (a *App) Start(invalidate func()) {
	a.mu.Lock()
	a.invalidate = invalidate
	a.mu.Unlock()

	go a.loadSelf()
	go a.loadPeers()
}

func (a *App) redraw() {
	a.mu.Lock()
	ask := a.invalidate
	a.mu.Unlock()

	if ask != nil {
		ask()
	}
}

// ---------------------------------------------------------------- loading

func (a *App) loadSelf() {
	me, err := a.from.Self()

	a.mu.Lock()
	if err == nil {
		a.me = me
	}
	a.mu.Unlock()
	a.redraw()
}

func (a *App) loadPeers() {
	a.setBusy(true)
	found, err := a.from.Peers()

	a.mu.Lock()
	a.busy = false
	if err != nil {
		a.trouble = err.Error()
	} else {
		a.peers, a.trouble = found, ""
	}
	a.mu.Unlock()
	a.redraw()
}

func (a *App) loadPaths(peer string) {
	a.setBusy(true)
	found, err := a.from.Spaces(peer)

	a.mu.Lock()
	a.busy = false
	if err != nil {
		a.paths, a.trouble = nil, err.Error()
	} else {
		a.paths, a.trouble = found, ""
	}
	a.mu.Unlock()
	a.redraw()
}

func (a *App) loadLog(peer string) {
	found, err := a.from.Log(peer)

	a.mu.Lock()
	if err != nil {
		a.trouble = err.Error()
	} else {
		a.history, a.trouble = found, ""
	}
	a.mu.Unlock()
	a.redraw()
}

func (a *App) say(peer, body string) {
	asLink := !strings.ContainsAny(body, " \t\n") &&
		(strings.HasPrefix(body, "http://") || strings.HasPrefix(body, "https://"))

	if err := a.from.Say(peer, body, asLink); err != nil {
		a.mu.Lock()
		a.trouble = err.Error()
		a.mu.Unlock()
		a.redraw()
		return
	}
	a.loadLog(peer)
}

func (a *App) setBusy(busy bool) {
	a.mu.Lock()
	a.busy = busy
	a.mu.Unlock()
	a.redraw()
}

// ---------------------------------------------------------------- where we are

func (a *App) peer() (Peer, bool) {
	if a.onPeer < 0 || a.onPeer >= len(a.peers) {
		return Peer{}, false
	}
	return a.peers[a.onPeer], true
}

func (a *App) path() (Space, bool) {
	if a.onPath < 0 || a.onPath >= len(a.paths) {
		return Space{}, false
	}
	return a.paths[a.onPath], true
}

func (a *App) enter(i int) {
	switch a.at {
	case atDevices:
		a.onPeer, a.at = i, atPaths
		a.paths = nil
		if with, ok := a.peer(); ok {
			go a.loadPaths(with.Name)
		}

	case atPaths:
		a.onPath = i
		on, okPath := a.path()
		with, okPeer := a.peer()
		if !okPath || !okPeer {
			return
		}
		if on.Kind == "branch" {
			return // it holds other paths and serves nothing
		}
		a.at = atOpen
		a.history = nil
		if on.Kind == "chat" {
			go a.loadLog(with.Name)
		}
	}
}

func (a *App) goBack() {
	switch a.at {
	case atOpen:
		a.at, a.history = atPaths, nil
	case atPaths:
		a.at, a.paths, a.trouble = atDevices, nil, ""
	}
}

// ---------------------------------------------------------------- drawing

// fill paints a rounded rectangle behind whatever is drawn into it.
//
// Recorded first and painted after, because the background has to be the size of the content
// and that is not known until the content has been laid out.
func fill(gtx layout.Context, c color.NRGBA, radius unit.Dp, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()

	shape := clip.RRect{
		Rect: image.Rectangle{Max: dims.Size},
		SE:   gtx.Dp(radius), SW: gtx.Dp(radius), NE: gtx.Dp(radius), NW: gtx.Dp(radius),
	}
	defer shape.Push(gtx.Ops).Pop()

	paint.Fill(gtx.Ops, c)
	call.Add(gtx.Ops)

	return dims
}

func when(at int64) string {
	return time.UnixMilli(at).Format("15:04")
}
