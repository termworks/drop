package tty

import (
	"io"
	"sync"
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
		writing := pacing(d)
		for chunk := range viewer.Frames() {
			if err := writing.write(chunk, stalledAfter); err != nil {
				sending <- err
				return
			}
		}
		sending <- d.Close()
	}()

	d.OnResize = resize

	if err := d.Pump(into); err != nil {
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

// paced writes to one watcher on a goroutine of its own, so that a write which is going nowhere can
// be given up on rather than pinning whoever handed it over.
type paced struct {
	chunks chan []byte
	sent   chan error
	// once, so that giving up twice is not a closed channel being closed again.
	once sync.Once
}

func pacing(to io.Writer) *paced {
	p := &paced{chunks: make(chan []byte), sent: make(chan error, 1)}

	go func() {
		for chunk := range p.chunks {
			_, err := to.Write(chunk)
			p.sent <- err
			if err != nil {
				return
			}
		}
	}()

	return p
}

// write hands over one chunk and waits the bound for it to land. Expiry ends this writer: the
// channel is closed, so the goroutine leaves as soon as the write it is stuck in gives way.
func (p *paced) write(chunk []byte, within time.Duration) error {
	timer := time.NewTimer(within)
	defer timer.Stop()

	select {
	case p.chunks <- chunk:
	case <-timer.C:
		p.give()
		return errStalled
	}

	select {
	case err := <-p.sent:
		return err
	case <-timer.C:
		p.give()
		return errStalled
	}
}

// give stops the writer. Whatever it is stuck in finishes into a channel nobody is reading, and
// then it leaves.
func (p *paced) give() {
	p.once.Do(func() { close(p.chunks) })
}
