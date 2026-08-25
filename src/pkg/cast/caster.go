// Package cast fans one terminal out to many watchers.
package cast

import (
	"sync"

	"github.com/bresilla/drop/src/pkg/term"
)

// Backlog is how many chunks may queue for one watcher before it is dropped. A terminal rendered
// with holes in it is worse than one that stopped, so a watcher that cannot keep up is cut loose
// rather than fed a corrupted stream.
const Backlog = 64

// Caster holds the live terminal and everyone watching it.
type Caster struct {
	mu      sync.Mutex
	viewers map[int]*Viewer
	nextID  int
	// stage is the terminal as it stands, kept so somebody joining is handed a picture rather than
	// a tail of bytes.
	//
	// Replaying recent output does not work for anything that draws by moving the cursor and
	// changing the cells that altered -- which is every full-screen program there is. The tail
	// holds whichever cells happened to change lately, so a watcher joining mid-run gets those and
	// nothing else: a screen with holes in it, filling in slowly as the program repaints.
	stage *term.Screen
	cols  uint16
	rows  uint16
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
	return &Caster{
		viewers: map[int]*Viewer{},
		stage:   term.New(int(cols), int(rows)),
		cols:    cols,
		rows:    rows,
	}
}

// Write records output and fans it out. It is an io.Writer so the pty can be copied straight into
// it.
func (c *Caster) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// The pty reuses its buffer, so what is handed on has to be this call's own copy.
	chunk := append([]byte(nil), p...)
	_, _ = c.stage.Write(chunk)

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

// Join adds a watcher, handing back the screen as it stands and the size of the terminal.
//
// The whole picture, drawn from the terminal this has been keeping, rather than the last however
// many bytes: what a watcher needs is what is on the screen now, and for anything that paints by
// moving the cursor those are not the same thing at all.
func (c *Caster) Join() (*Viewer, []byte, uint16, uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	v := &Viewer{id: c.nextID, out: make(chan []byte, Backlog)}
	c.viewers[v.id] = v

	return v, c.picture(), c.cols, c.rows
}

// picture is the screen as it stands, as the bytes that draw it: home the cursor, clear, and paint.
func (c *Caster) picture() []byte {
	if c.stage == nil {
		return nil
	}
	return append([]byte("\x1b[H\x1b[2J"), c.stage.ANSI()...)
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
	if cols < 1 || rows < 1 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.cols, c.rows = cols, rows
	if c.stage != nil {
		c.stage.Resize(int(cols), int(rows))
	}
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

// Clear throws the picture away, so whoever joins next is handed a blank screen.
//
// What a password prompt requires. Detection cannot precede the prompt: the bytes that drew
// `Password:` were already on the screen before the terminal's echo flag changed. Pausing would
// leave them there for the next watcher to be given.
func (c *Caster) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stage = term.New(int(c.cols), int(c.rows))
}
