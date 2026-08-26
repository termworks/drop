package live

import (
	"errors"
	"io"
	"sync"
	"time"
)

// ErrStalled says the far end took nothing for long enough to be given up on.
var ErrStalled = errors.New("the far end stopped reading")

// Paced writes to the far end on a goroutine of its own, so a write that is going nowhere can be
// given up on rather than pinning whoever handed it over.
//
// A stream both ends write on cannot tell a reader that has stopped reading from one that is merely
// quiet: the write simply never lands. What is behind it — a terminal shared with other watchers, a
// command still running — is not free to wait for ever.
type Paced struct {
	to     io.Writer
	within time.Duration
	chunks chan []byte
	sent   chan error
	// gone is closed once this writer has been given up on, so a caller that keeps writing is told
	// rather than handing chunks to a goroutine that has left.
	gone chan struct{}
	once sync.Once
}

// Pacing runs a paced writer over to, giving up on any chunk that has not landed within.
func Pacing(to io.Writer, within time.Duration) *Paced {
	p := &Paced{
		to:     to,
		within: within,
		chunks: make(chan []byte),
		sent:   make(chan error, 1),
		gone:   make(chan struct{}),
	}

	go func() {
		for {
			select {
			case chunk := <-p.chunks:
				_, err := p.to.Write(chunk)
				select {
				case p.sent <- err:
				default:
				}
				if err != nil {
					return
				}
			case <-p.gone:
				return
			}
		}
	}()

	return p
}

// Write hands over one chunk and waits the bound for it to land. Expiry ends this writer: whatever
// it is stuck in finishes into a channel nobody is reading, and then it leaves.
func (p *Paced) Write(chunk []byte) (int, error) {
	select {
	case <-p.gone:
		return 0, ErrStalled
	default:
	}

	// The chunk outlives this call, and a caller writing out of a buffer it reuses would have it
	// overwritten underneath the goroutine.
	held := append([]byte(nil), chunk...)

	timer := time.NewTimer(p.within)
	defer timer.Stop()

	select {
	case p.chunks <- held:
	case <-timer.C:
		p.Give()
		return 0, ErrStalled
	}

	select {
	case err := <-p.sent:
		if err != nil {
			return 0, err
		}
		return len(chunk), nil
	case <-timer.C:
		p.Give()
		return 0, ErrStalled
	}
}

// Give stops the writer, whether or not it stalled. It is safe to call twice.
func (p *Paced) Give() {
	p.once.Do(func() { close(p.gone) })
}
