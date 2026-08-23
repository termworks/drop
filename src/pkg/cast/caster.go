// Package cast fans one terminal out to many watchers.
package cast

import "sync"

// Scrollback is how much recent output is kept for someone who joins mid-session. A terminal is a
// stream of escape sequences, so a watcher that starts from nothing sees a blank screen until the
// next redraw; replaying the tail gives it something to render immediately.
const Scrollback = 128 << 10

// Backlog is how many chunks may queue for one watcher before it is dropped. A terminal rendered
// with holes in it is worse than one that stopped, so a watcher that cannot keep up is cut loose
// rather than fed a corrupted stream.
const Backlog = 64

// Caster holds the live terminal and everyone watching it.
type Caster struct {
	mu      sync.Mutex
	viewers map[int]*Viewer
	nextID  int
	history []byte
	cols    uint16
	rows    uint16
}

// Viewer is one watcher's feed.
type Viewer struct {
	id  int
	out chan []byte
}

// Frames is what to write to this watcher, in order. It is closed when the cast ends or the
// watcher is dropped.
func (v *Viewer) Frames() <-chan []byte {
	return v.out
}

func New(cols, rows uint16) *Caster {
	return &Caster{viewers: map[int]*Viewer{}, cols: cols, rows: rows}
}

// Write records output and fans it out. It is an io.Writer so the pty can be copied straight into
// it.
func (c *Caster) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// The pty reuses its buffer, so what is handed on has to be this call's own copy.
	chunk := append([]byte(nil), p...)
	c.remember(chunk)

	for id, v := range c.viewers {
		select {
		case v.out <- chunk:
		default:
			delete(c.viewers, id)
			close(v.out)
		}
	}
	return len(p), nil
}

func (c *Caster) remember(chunk []byte) {
	c.history = append(c.history, chunk...)
	if len(c.history) > Scrollback {
		c.history = append([]byte(nil), c.history[len(c.history)-Scrollback:]...)
	}
}

// Join adds a watcher, handing back the recent output it should render first and the size of the
// terminal it is watching.
func (c *Caster) Join() (*Viewer, []byte, uint16, uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	v := &Viewer{id: c.nextID, out: make(chan []byte, Backlog)}
	c.viewers[v.id] = v

	return v, append([]byte(nil), c.history...), c.cols, c.rows
}

// Leave drops a watcher. Safe to call after it was already dropped for lagging.
func (c *Caster) Leave(v *Viewer) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, live := c.viewers[v.id]; live {
		delete(c.viewers, v.id)
		close(v.out)
	}
}

// Resize records the terminal's new shape, for watchers that join later.
func (c *Caster) Resize(cols, rows uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cols, c.rows = cols, rows
}

// Size is the terminal's current shape.
func (c *Caster) Size() (uint16, uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.cols, c.rows
}

// Watching is how many watchers are attached.
func (c *Caster) Watching() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.viewers)
}

// Stop ends every feed, which is what tells each watcher the cast is over.
func (c *Caster) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, v := range c.viewers {
		delete(c.viewers, id)
		close(v.out)
	}
}

// Clear throws the scrollback away.
//
// What a password prompt requires. Detection cannot precede the prompt: the bytes that drew
// `Password:` went out before the terminal's echo flag changed, so they are already recorded.
// Pausing would leave them there for the next watcher to be handed.
func (c *Caster) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.history = nil
}
