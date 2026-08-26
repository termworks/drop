package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

const (
	// hangUpWithin is how long a shell has to leave after its terminal is hung up, before it is
	// killed outright.
	hangUpWithin = 2 * time.Second
	// stalledAfter is how long one chunk may take to reach a watcher before that watcher is given
	// up on. A window that has stopped reading takes nothing at all, and the shell behind it is
	// shared with everybody else looking at it.
	stalledAfter = 10 * time.Second
	// partingWithin bounds the wait for a watcher's feed to finish once the far end has stopped
	// writing, the way a duplex bounds its own linger.
	partingWithin = 5 * time.Second
)

// errStalled says a watcher took nothing for long enough to be dropped.
var errStalled = errors.New("a watcher stopped reading")

// terminals holds one live shell per tty namespace.
//
// One per namespace rather than one per caller: a tty namespace is a terminal being shared, so two
// watchers have to see the same one. A shell each would be a different feature.
type terminals struct {
	mu   sync.Mutex
	open map[string]*terminal
}

func newTerminals() *terminals {
	return &terminals{open: map[string]*terminal{}}
}

// terminal is a shell running in a pty, fanned out to everyone watching it.
type terminal struct {
	stage *cast.Caster
	ptmx  *os.File
	shell *exec.Cmd
	// reaped is closed once the shell has ended and been waited for.
	reaped chan struct{}
}

// at returns the terminal for a namespace, starting it on the first watcher.
func (t *terminals) at(mount ns.Mount) (*terminal, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if live, ok := t.open[mount.Path]; ok {
		return live, nil
	}

	shell := mount.Shell
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", mount.Path, err)
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: 80, Rows: 24})

	live := &terminal{
		stage:  cast.New(80, 24),
		ptmx:   ptmx,
		shell:  cmd,
		reaped: make(chan struct{}),
	}
	t.open[mount.Path] = live

	// Everything the shell writes goes to every watcher, and into the scrollback so somebody
	// arriving later has something to render.
	go func() {
		_, _ = io.Copy(live.stage, ptmx)

		// The shell is gone: end every feed, wait for the process so it is not left a zombie, and
		// let the next watcher start a fresh one.
		live.stage.Stop()
		_ = ptmx.Close()
		_ = cmd.Wait()
		close(live.reaped)

		t.mu.Lock()
		delete(t.open, mount.Path)
		t.mu.Unlock()
	}()

	return live, nil
}

// stop ends every terminal, which is what ends the watchers attached to them.
func (t *terminals) stop() {
	t.mu.Lock()
	live := make([]*terminal, 0, len(t.open))
	for path, at := range t.open {
		live = append(live, at)
		delete(t.open, path)
	}
	t.mu.Unlock()

	// Outside the lock: ending a terminal waits for the goroutine reading it, and that goroutine
	// takes this lock on its way out.
	for _, at := range live {
		at.end()
	}
}

// end takes a shell down: the whole process group is hung up, killed if it will not go, and the pty
// closed behind it.
//
// The group rather than the shell alone, because whatever the shell started is what holds the
// terminal open — a hangup delivered to the shell and nothing else leaves its children running with
// a pty nobody is reading.
func (live *terminal) end() {
	live.stage.Stop()
	live.hangUp()
	_ = live.ptmx.Close()
}

// hangUp sends the shell's process group away, and waits a little before insisting.
func (live *terminal) hangUp() {
	if live.shell == nil || live.shell.Process == nil {
		return
	}

	// A shell that has already been waited for has no pid worth signalling: the number is the
	// system's to hand out again.
	select {
	case <-live.reaped:
		return
	default:
	}

	group := -live.shell.Process.Pid
	_ = syscall.Kill(group, syscall.SIGHUP)

	select {
	case <-live.reaped:
	case <-time.After(hangUpWithin):
		_ = syscall.Kill(group, syscall.SIGKILL)
	}
}

// serveTTY attaches one watcher to the namespace's terminal.
func serveTTY(at proto.Resolved, d *proto.Duplex, live *terminal) error {
	viewer, replay, cols, rows := live.stage.Join()
	defer live.stage.Leave(viewer)

	if err := d.Resize(cols, rows); err != nil {
		return err
	}
	// Clear the watcher's screen before the replay, so the tail of the scrollback lands on a blank
	// terminal rather than on top of whatever was there.
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

	// What a watcher types reaches the shell only when the namespace said it may.
	into := io.Writer(io.Discard)
	if at.Mount.Input {
		into = live.ptmx
	}

	// Its shape reaches the shell either way. A watcher that cannot type still has a window, and a
	// pty drawing for a size nobody is looking at wraps every line in the wrong place — which is
	// what a read-only terminal looked like before.
	//
	// One shell, so the last watcher to resize decides. That is what sharing a terminal means.
	d.OnResize = func(c, r uint16) {
		_ = pty.Setsize(live.ptmx, &pty.Winsize{Cols: c, Rows: r})
		live.stage.Resize(c, r)
	}

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
