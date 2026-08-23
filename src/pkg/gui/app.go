//go:build js || android

package gui

import (
	"image"
	"image/color"
	"strings"
	"sync"
	"time"

	tickets "github.com/bresilla/drop/src/pkg/ticket"
	"rsc.io/qr"

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

	// pending is something the share sheet handed over, waiting to be told where it goes.
	pending *Shared

	// live is the terminal being watched, when the open path is one.
	live *watching

	// linking is a pairing being offered, shown until it finishes or is put away.
	linking *linking
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
	sendShared widget.Clickable
	dropShared widget.Clickable
	pairNow    widget.Clickable
	pairStop   widget.Clickable
	joinNow    widget.Clickable
	joinGo     widget.Clickable
	joinField  widget.Editor
	pairScroll layout.List
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
	go a.claimShare()
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
		switch on.Kind {
		case "chat":
			go a.loadLog(with.Name)
		case "tty", "stream":
			// Sized to something ordinary until the far end says what it actually is.
			a.live = startWatching(a.from, with.Name, on.Path, 80, 24, a.redraw)
		}
	}
}

func (a *App) goBack() {
	switch a.at {
	case atOpen:
		a.endWatch()
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

// claimShare takes whatever was shared to this device from outside.
//
// Only a Remote has one: a share reaches a browser through the bridge that redirected here. A phone
// is handed its own directly, which is a different path and not this one.
func (a *App) claimShare() {
	taker, ok := a.from.(interface{ Claim() (*Shared, error) })
	if !ok {
		return
	}

	item, err := taker.Claim()
	if err != nil {
		a.mu.Lock()
		a.trouble = err.Error()
		a.mu.Unlock()
		a.redraw()
		return
	}
	if item == nil {
		return
	}

	a.mu.Lock()
	a.pending = item
	a.mu.Unlock()
	a.redraw()
}

// deliverPending sends what was shared into the conversation that was just opened, which is the
// answer to the question the share sheet could not ask.
func (a *App) deliverPending(peer string) {
	a.mu.Lock()
	item := a.pending
	a.mu.Unlock()

	if item == nil {
		return
	}
	if item.Name != "" {
		a.mu.Lock()
		a.trouble = "sending a file from a share is not wired up yet"
		a.mu.Unlock()
		a.redraw()
		return
	}

	if err := a.from.Say(peer, item.Body(), item.IsLink()); err != nil {
		a.mu.Lock()
		a.trouble = err.Error()
		a.mu.Unlock()
		a.redraw()
		return
	}

	a.mu.Lock()
	a.pending, a.trouble = nil, ""
	a.mu.Unlock()

	a.loadLog(peer)
}

// endWatch stops reading a terminal, so leaving a path does not leave one being read by nobody.
func (a *App) endWatch() {
	if a.live != nil {
		a.live.end()
		a.live = nil
	}
}

// linking is a pairing on screen: the ticket to use, and the code to point a camera at.
type linking struct {
	ticket string
	code   *qr.Code
	with   string
	err    string
}

// startPairing opens this device up and puts the code on screen.
func (a *App) startPairing() {
	ticket, err := a.from.Offer()
	if err != nil {
		a.mu.Lock()
		a.linking = &linking{err: err.Error()}
		a.mu.Unlock()
		a.redraw()
		return
	}

	at := &linking{ticket: ticket}
	if drawn, err := tickets.Code(ticket); err == nil {
		at.code = drawn
	}

	a.mu.Lock()
	a.linking = at
	a.mu.Unlock()
	a.redraw()

	a.awaitPairing()
}

// awaitPairing watches the offer until somebody answers it or it is put away.
func (a *App) awaitPairing() {
	for range 600 {
		time.Sleep(time.Second)

		a.mu.Lock()
		open := a.linking != nil
		a.mu.Unlock()
		if !open {
			return
		}

		_, with, err := a.from.Pairing()
		if err != nil || with == "" {
			continue
		}

		a.mu.Lock()
		a.linking = nil
		a.mu.Unlock()

		a.loadPeers()
		return
	}
}

// stopPairing takes the offer down, so a code shown by mistake stops being one.
func (a *App) stopPairing() {
	a.mu.Lock()
	a.linking = nil
	a.mu.Unlock()

	go func() { _ = a.from.Unpair() }()
	a.redraw()
}
