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
	// follow, when set, is told each shape the terminal takes from its watchers. Unset, the shape is
	// whatever it was given, which is what a recording wants.
	follow func(cols, rows uint16)
	// stopped says the cast is over. Everything after that is a no-op, and whoever joins is handed
	// a feed that is already closed rather than one nothing will ever close.
	stopped bool
}

// Viewer is one watcher's feed.
type Viewer struct {
	id  int
	out chan []byte
	// changed says the terminal's shape, or who is watching it, is not what this watcher was last
	// told.
	changed chan struct{}
	// cols and rows are the window this watcher has, once it has said; zero until then.
	cols, rows uint16
}

// Changed fires when the terminal changes shape or somebody joins or leaves it — which somebody
// else watching may be the cause of.
func (v *Viewer) Changed() <-chan struct{} {
	return v.changed
}

// Frames is what to write to this watcher, in order. It is closed when the cast ends or the
// watcher is dropped.
func (v *Viewer) Frames() <-chan []byte {
	return v.out
}

func New(cols, rows int) *Caster {
	stage := term.New(cols, rows)
	cols, rows = stage.Size()
	return &Caster{
		viewers: map[int]*Viewer{},
		stage:   stage,
		cols:    uint16(cols),
		rows:    uint16(rows),
	}
}

// Write records output and fans it out. It is an io.Writer so the pty can be copied straight into
// it.
func (c *Caster) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		return len(p), nil
	}

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
//
// Joining a cast that has stopped hands back a feed that is already closed, so whoever is watching
// it reads nothing and finishes rather than waiting on a channel with nobody behind it.
func (c *Caster) Join() (*Viewer, []byte, uint16, uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		over := make(chan []byte)
		close(over)
		return &Viewer{out: over}, nil, c.cols, c.rows
	}

	c.nextID++
	v := &Viewer{id: c.nextID, out: make(chan []byte, Backlog), changed: make(chan struct{}, 1)}
	c.viewers[v.id] = v
	c.notify()

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
		c.reshape()
		c.notify()
	}
}

// Resize gives the terminal a new shape: the one a recording says it had, say.
func (c *Caster) Resize(cols, rows uint16) {
	if cols < 1 || rows < 1 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		return
	}
	c.resize(cols, rows)
}

// resize is Resize with the lock held. Every watcher is told, whoever the change was for.
func (c *Caster) resize(cols, rows uint16) {
	if c.stage != nil {
		c.stage.Resize(int(cols), int(rows))
		boundedCols, boundedRows := c.stage.Size()
		cols, rows = uint16(boundedCols), uint16(boundedRows)
	}
	c.cols, c.rows = cols, rows
	c.notify()
}

// Follow makes the terminal's shape follow the windows watching it: as big as the smallest of them,
// so everybody sees all of it and nobody a corner. apply is told each shape it takes, which is how
// the program behind it hears.
//
// The smallest rather than the last to ask. The last to ask won when this was written, and two
// watchers with different windows took the terminal from each other on every resize — each seeing
// it drawn for the other, one of them with every row wrapped.
func (c *Caster) Follow(apply func(cols, rows uint16)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.follow = apply
	c.reshape()
}

// Want says how big one watcher's window is.
func (c *Caster) Want(v *Viewer, cols, rows uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped || c.viewers[v.id] != v {
		return
	}
	v.cols, v.rows = cols, rows
	c.reshape()
}

// reshape fits a terminal that follows its watchers to the smallest window among them. A terminal
// nobody has said anything about keeps the shape it has.
func (c *Caster) reshape() {
	if c.follow == nil {
		return
	}

	var cols, rows uint16
	windows := 0
	for _, v := range c.viewers {
		if v.cols == 0 || v.rows == 0 {
			continue
		}
		windows++
		if cols == 0 || v.cols < cols {
			cols = v.cols
		}
		if rows == 0 || v.rows < rows {
			rows = v.rows
		}
	}
	if cols == 0 || rows == 0 {
		return
	}

	// Shared, it is never held below the size terminals are made at. One small window would
	// otherwise take everybody else's with it — a phone held upright, or a pane split in two,
	// shrinking a program others are using to a size it refuses to draw at. The small window is
	// the one that crops. Watched by one, the terminal is that window's, whatever size it is.
	if windows > 1 {
		cols, rows = max(cols, leastCols), max(rows, leastRows)
	}
	if cols == c.cols && rows == c.rows {
		return
	}
	c.resize(cols, rows)
	c.follow(c.cols, c.rows)
}

// notify tells every watcher something about the terminal changed, without waiting for any of
// them: one that has not caught up already has a change pending, and one means the same as two.
func (c *Caster) notify() {
	for _, v := range c.viewers {
		select {
		case v.changed <- struct{}{}:
		default:
		}
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

// Stop ends every feed, which is what tells each watcher the cast is over. Nothing written or
// resized afterwards goes anywhere.
func (c *Caster) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stopped = true
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

// leastCols and leastRows are the size a terminal is made at, and the smallest a shared one is held
// to.
const (
	leastCols = 80
	leastRows = 24
)
