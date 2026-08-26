// Package tty is a terminal being shared: one shell per namespace, shown to everybody watching it.
//
// One shell rather than one each, because a tty namespace is a terminal being shared and two
// watchers have to see the same one. What a watcher may type is the mount's decision; its shape
// reaches the shell either way, since a pty drawing for a size nobody is looking at wraps every
// line in the wrong place.
package tty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/live"
	"github.com/bresilla/drop/src/pkg/node"
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

// Config is what a tty namespace was told: what to start, and whether the far end may type.
type Config struct {
	// Shell is what this namespace starts; empty means $SHELL.
	Shell string
	// Input lets the far end type into it.
	Input bool
}

// Into is what the process running a tty hands it.
type Into struct {
	// Watched, when set, is told that somebody joined, and how many are on it now.
	Watched func(path string, from node.ID, watching int)
	// Showing, when set, is asked whether a path is a screen that is already running — somebody
	// casting rather than a shell to start. The second result says the path is that kind of
	// terminal; a nil screen with it means nobody is casting just now.
	Showing func(path string) (*cast.Caster, bool)
}

// TTY serves terminals.
//
// One live shell per namespace, held here: the map outlives any one session, which is what makes
// the second watcher of a path join the terminal the first one started.
type TTY struct {
	into Into
	mu   sync.Mutex
	open map[string]*terminal
}

func New(into Into) *TTY {
	return &TTY{into: into, open: map[string]*terminal{}}
}

func (t *TTY) Name() string { return "tty" }
func (t *TTY) Version() int { return 1 }

// Read takes the shell to start and whether the far end may type into it.
func (t *TTY) Read(d arch.Declared) (arch.Config, error) {
	shell, _ := d.String("shell")
	input, _ := d.Bool("input")
	return Config{Shell: shell, Input: input}, nil
}

func (t *TTY) Note(c arch.Config) arch.Note {
	cfg, _ := c.(Config)

	detail, about := "read-only", "a terminal, as it is being used"
	if cfg.Input {
		detail, about = "interactive", "a terminal, as it is being used, and you may type"
	}
	return arch.Note{
		Writable: cfg.Input,
		Detail:   detail,
		About:    about,
		Glyph:    "▮",
	}
}

// Serve attaches one watcher to the namespace's terminal.
func (t *TTY) Serve(ctx context.Context, at arch.Session) error {
	cfg, _ := at.Config.(Config)
	d := live.New(at.Conn, at.Stream)

	// A screen somebody is already casting is a terminal like any other, but it is being fed from
	// elsewhere rather than started here, so it is answered before a shell is looked for.
	if t.into.Showing != nil {
		if stage, cast := t.into.Showing(at.Path); cast {
			if stage == nil {
				return fmt.Errorf("nothing is being cast")
			}
			t.watched(at, stage.Watching()+1)
			return attach(d, stage, io.Discard, nil)
		}
	}

	term, err := t.at(at.Path, cfg)
	if err != nil {
		return err
	}
	t.watched(at, term.stage.Watching()+1)

	// What a watcher types reaches the shell only when the namespace said it may.
	into := io.Writer(io.Discard)
	if cfg.Input {
		into = term.ptmx
	}

	// One shell, so the last watcher to resize decides. That is what sharing a terminal means.
	resize := func(cols, rows uint16) {
		_ = pty.Setsize(term.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
		term.stage.Resize(cols, rows)
	}
	return attach(d, term.stage, into, resize)
}

func (t *TTY) watched(at arch.Session, total int) {
	if t.into.Watched != nil {
		t.into.Watched(at.Path, at.From, total)
	}
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
func (t *TTY) at(path string, cfg Config) (*terminal, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if live, ok := t.open[path]; ok {
		return live, nil
	}

	shell := cfg.Shell
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: 80, Rows: 24})

	term := &terminal{
		stage:  cast.New(80, 24),
		ptmx:   ptmx,
		shell:  cmd,
		reaped: make(chan struct{}),
	}
	t.open[path] = term

	// Everything the shell writes goes to every watcher, and into the scrollback so somebody
	// arriving later has something to render.
	go func() {
		_, _ = io.Copy(term.stage, ptmx)

		// The shell is gone: end every feed, wait for the process so it is not left a zombie, and
		// let the next watcher start a fresh one.
		term.stage.Stop()
		_ = ptmx.Close()
		_ = cmd.Wait()
		close(term.reaped)

		t.mu.Lock()
		delete(t.open, path)
		t.mu.Unlock()
	}()

	return term, nil
}

// Stop ends every terminal, which is what ends the watchers attached to them.
func (t *TTY) Stop() {
	t.mu.Lock()
	open := make([]*terminal, 0, len(t.open))
	for path, at := range t.open {
		open = append(open, at)
		delete(t.open, path)
	}
	t.mu.Unlock()

	// Outside the lock: ending a terminal waits for the goroutine reading it, and that goroutine
	// takes this lock on its way out.
	for _, at := range open {
		at.end()
	}
}

// end takes a shell down: the whole process group is hung up, killed if it will not go, and the pty
// closed behind it.
//
// The group rather than the shell alone, because whatever the shell started is what holds the
// terminal open — a hangup delivered to the shell and nothing else leaves its children running with
// a pty nobody is reading.
func (term *terminal) end() {
	term.stage.Stop()
	term.hangUp()
	_ = term.ptmx.Close()
}

// hangUp sends the shell's process group away, and waits a little before insisting.
func (term *terminal) hangUp() {
	if term.shell == nil || term.shell.Process == nil {
		return
	}

	// A shell that has already been waited for has no pid worth signalling: the number is the
	// system's to hand out again.
	select {
	case <-term.reaped:
		return
	default:
	}

	group := -term.shell.Process.Pid
	_ = syscall.Kill(group, syscall.SIGHUP)

	select {
	case <-term.reaped:
	case <-time.After(hangUpWithin):
		_ = syscall.Kill(group, syscall.SIGKILL)
	}
}
