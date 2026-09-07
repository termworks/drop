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
func attach(ctx context.Context, d *live.Duplex, stage *cast.Caster, into io.Writer, resize func(cols, rows uint16)) error {
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

	d.OnResize = resize

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
