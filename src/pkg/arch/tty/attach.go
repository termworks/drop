package tty

import (
	"io"
	"time"

	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/live"
)

// attach feeds one watcher from a screen and reads back whatever it types.
//
// The screen is cleared before the replay, so the tail of the scrollback lands on a blank terminal
// rather than on top of whatever was there.
func attach(d *live.Duplex, stage *cast.Caster, into io.Writer, resize func(cols, rows uint16)) error {
	viewer, replay, cols, rows := stage.Join()
	defer stage.Leave(viewer)

	if err := d.Resize(cols, rows); err != nil {
		return err
	}
	if _, err := d.Write([]byte("\x1b[2J\x1b[H")); err != nil {
		return err
	}
	if len(replay) > 0 {
		if _, err := d.Write(replay); err != nil {
			return err
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
			return err
		}
	case err := <-sending:
		d.Stop()
		return err
	}

	// Bounded, because the far end having stopped writing says nothing about whether it is still
	// reading, and a watcher that is not costs one serving goroutine for as long as it stays.
	select {
	case err := <-sending:
		return err
	case <-time.After(partingWithin):
		return nil
	}
}
