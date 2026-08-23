package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"

	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

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

	live := &terminal{stage: cast.New(80, 24), ptmx: ptmx}
	t.open[mount.Path] = live

	// Everything the shell writes goes to every watcher, and into the scrollback so somebody
	// arriving later has something to render.
	go func() {
		_, _ = io.Copy(live.stage, ptmx)

		// The shell is gone: end every feed, and let the next watcher start a fresh one.
		live.stage.Stop()
		_ = ptmx.Close()

		t.mu.Lock()
		delete(t.open, mount.Path)
		t.mu.Unlock()
	}()

	return live, nil
}

// stop closes every terminal, which is what ends the watchers attached to them.
func (t *terminals) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for path, live := range t.open {
		live.stage.Stop()
		_ = live.ptmx.Close()
		delete(t.open, path)
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
		for chunk := range viewer.Frames() {
			if _, err := d.Write(chunk); err != nil {
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
		d.OnResize = func(c, r uint16) {
			_ = pty.Setsize(live.ptmx, &pty.Winsize{Cols: c, Rows: r})
			live.stage.Resize(c, r)
		}
	}

	if err := d.Pump(into); err != nil {
		return err
	}
	return <-sending
}
