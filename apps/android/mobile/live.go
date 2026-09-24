package mobile

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/term"
	"github.com/bresilla/drop/src/pkg/tui"
)

// Screen is what the app implements to show a live path: a terminal, or a stream.
type Screen interface {
	// Drawn is the whole screen as it stands, as JSON: cols, rows, the lines as styled runs keyed
	// by row, and where the cursor is.
	Drawn(frame string)
	// Ended says the path has stopped, and why; empty when it simply ended.
	Ended(why string)
	// Company says who is on the terminal: how many are watching, and whether the shell is this
	// device's alone.
	Company(watching int, own bool)
}

// frameEvery bounds how often a screen is repainted. A terminal redrawing flat out would otherwise
// cost a frame per write, and a phone is not the place to spend that.
const frameEvery = 66 * time.Millisecond

// Live is a live path being watched.
type Live struct {
	cancel context.CancelFunc

	mu     sync.Mutex
	screen *term.Screen
	talk   tui.Talk
	// wantCols and wantRows are the size last asked for, kept for a far end that was not ready.
	wantCols, wantRows int
	nudge              chan struct{}
}

// Watch opens a live path on a machine and draws it into screen until Stop. cols and rows are the
// size to draw at until the far end says what size it is.
func (n *Node) Watch(name, path, archetype string, cols, rows int, into Screen) (*Live, error) {
	on, err := entry(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(n.ctx)

	l := &Live{
		cancel: cancel,
		screen: term.New(max(cols, 1), max(rows, 1)),
		nudge:  make(chan struct{}, 1),
	}
	go l.paint(ctx, into)

	go func() {
		err := n.back.Watch(ctx, tui.Watching{
			On:        on,
			Path:      path,
			Archetype: archetype,
			Into:      l,
			Sized:     l.sized,
			Told:      into.Company,
			Ready:     l.ready,
		})
		cancel()
		why := ""
		if err != nil && ctx.Err() == nil {
			why = err.Error()
		}
		into.Ended(why)
	}()
	return l, nil
}

func (l *Live) Write(p []byte) (int, error) {
	l.mu.Lock()
	n, err := l.screen.Write(p)
	l.mu.Unlock()

	select {
	case l.nudge <- struct{}{}:
	default:
	}
	return n, err
}

// sized follows the far end's terminal. A command rather than a pty has no size and says zero,
// which is left alone rather than taken literally.
func (l *Live) sized(cols, rows int) {
	if cols < 1 || rows < 1 {
		return
	}
	l.mu.Lock()
	l.screen.Resize(cols, rows)
	l.mu.Unlock()
}

// ready is the far end saying it can be spoken to, and any size asked for before then is given now.
func (l *Live) ready(talk tui.Talk) {
	l.mu.Lock()
	l.talk = talk
	cols, rows := l.wantCols, l.wantRows
	l.mu.Unlock()

	if cols > 0 && rows > 0 {
		_ = talk.Resize(cols, rows)
	}
}

func (l *Live) paint(ctx context.Context, into Screen) {
	tick := time.NewTicker(frameEvery)
	defer tick.Stop()

	dirty := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.nudge:
			dirty = true
		case <-tick.C:
			if !dirty {
				continue
			}
			dirty = false

			l.mu.Lock()
			frame := term.NewPainter().Frame(l.screen)
			l.mu.Unlock()

			body, err := json.Marshal(frame)
			if err == nil {
				into.Drawn(string(body))
			}
		}
	}
}

// Type sends keystrokes. Whether they are taken is the far end's decision.
func (l *Live) Type(text string) error {
	l.mu.Lock()
	talk := l.talk
	l.mu.Unlock()

	if talk == nil || text == "" {
		return nil
	}
	return talk.Type([]byte(text))
}

// Resize asks the far end for another size, which a shared terminal may refuse. Asked before the
// far end is ready, it is remembered and asked for as soon as it is.
func (l *Live) Resize(cols, rows int) error {
	l.mu.Lock()
	l.wantCols, l.wantRows = cols, rows
	talk := l.talk
	l.mu.Unlock()

	if talk == nil {
		return nil
	}
	return talk.Resize(cols, rows)
}

// Stop ends the watch.
func (l *Live) Stop() { l.cancel() }
