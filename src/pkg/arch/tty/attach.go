package tty

import (
	"context"
	"io"
	"time"

	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/live"
)

// attach feeds one watcher from a screen and reads back whatever it types.
//
// The screen is cleared before the replay, so the tail of the scrollback lands on a blank terminal
// rather than on top of whatever was there.
//
// own says the terminal is this watcher's alone, and follows says its shape is taken from the
// windows watching it rather than fixed, which a recording is.
func attach(ctx context.Context, d *live.Duplex, stage *cast.Caster, into io.Writer, own, follows bool) error {
	viewer, replay, cols, rows := stage.Join()
	defer stage.Leave(viewer)
	over := make(chan struct{})
	defer close(over)
	go func() {
		select {
		case <-ctx.Done():
			d.Stop()
			d.StopWrite()
		case <-over:
		}
	}()

	if err := d.Resize(int(cols), int(rows)); err != nil {
		return attachError(ctx, err)
	}
	watching := stage.Watching()
	if err := d.Tell(live.Company{Watching: watching, Own: own}); err != nil {
		return attachError(ctx, err)
	}
	if _, err := d.Write([]byte("\x1b[2J\x1b[H")); err != nil {
		return attachError(ctx, err)
	}
	if len(replay) > 0 {
		if _, err := d.Write(replay); err != nil {
			return attachError(ctx, err)
		}
	}

	sending := make(chan error, 1)
	go func() {
		writing := live.Pacing(d, stalledAfter)
		defer writing.Give()

		for chunk := range viewer.Frames() {
			if _, err := writing.Write(chunk); err != nil {
				sending <- err
				return
			}
		}
		sending <- d.Close()
	}()

	// A window's size is taken as what this watcher wants the terminal to be, when the terminal
	// follows its watchers. A recording has the shape it was made at, whatever anybody's window is.
	if follows {
		d.OnResize = func(cols, rows uint16) { stage.Want(viewer, cols, rows) }
	}

	// Every change is passed on: the terminal's shape, which somebody else joining, leaving or
	// resizing can change, and how many are on it. Told only on joining, a watcher went on drawing
	// into the shape it was handed while the program drew for another, and every row wrapped.
	go func() {
		toldCols, toldRows, toldWatching := cols, rows, watching
		for {
			select {
			case <-over:
				return
			case <-viewer.Changed():
			}
			if cols, rows := stage.Size(); cols != toldCols || rows != toldRows {
				toldCols, toldRows = cols, rows
				_ = d.Resize(int(cols), int(rows))
			}
			// A terminal closing under its watchers counts nobody on it. This watcher is still
			// here to be told, so it is never told fewer than itself.
			if now := max(stage.Watching(), 1); now != toldWatching {
				toldWatching = now
				_ = d.Tell(live.Company{Watching: now, Own: own})
			}
		}
	}()

	// A watcher that went without saying so — a window closed, a laptop shut — is noticed by its
	// silence rather than by the transport giving up on it half a minute later. Until then it
	// counted as watching, and it went on holding the terminal to its window's size.
	go func() {
		tick := time.NewTicker(pingEvery)
		defer tick.Stop()
		for {
			select {
			case <-over:
				return
			case <-tick.C:
			}
			if d.Quiet() > goneAfter {
				d.Stop()
				return
			}
			_ = d.Ping()
		}
	}()

	// Both directions are waited on, because either one ending ends the session. A watcher whose
	// feed has stopped is not watching the terminal any more, whatever it is still typing into it,
	// and the read side is ended so the pump comes back rather than sitting on a peer that has no
	// reason to say anything else.
	pumped := make(chan error, 1)
	go func() { pumped <- d.Pump(into) }()

	select {
	case err := <-pumped:
		if err != nil {
			return attachError(ctx, err)
		}
	case err := <-sending:
		d.Stop()
		return attachError(ctx, err)
	case <-ctx.Done():
		return ctx.Err()
	}

	// Bounded, because the far end having stopped writing says nothing about whether it is still
	// reading, and a watcher that is not costs one serving goroutine for as long as it stays.
	select {
	case err := <-sending:
		return attachError(ctx, err)
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(partingWithin):
		return nil
	}
}

func attachError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// pingEvery is how often a watcher is asked to say something, and goneAfter how long it may say
// nothing before it is taken to have gone.
const (
	pingEvery = 5 * time.Second
	goneAfter = 3 * pingEvery
)
