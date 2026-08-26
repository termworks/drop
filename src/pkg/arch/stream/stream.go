// Package stream runs a command and hands over what it writes.
//
// Nothing knows how long it will run or how much it will say, which is the point: a stream
// namespace is a log being followed, a sensor being read, a build going past.
package stream

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/live"
	"github.com/bresilla/drop/src/pkg/node"
)

const (
	// stalledAfter is how long one chunk may take to reach the far end before it is given up on. A
	// reader that has stopped reading takes nothing at all, and the command behind it keeps running
	// for as long as anything waits on it.
	stalledAfter = 10 * time.Second
	// partingWithin bounds the wait for the read side to come back once the command is done.
	partingWithin = 5 * time.Second
)

// Config is what a stream namespace was told: the command it runs and reads.
type Config struct {
	Command string
}

// Into is what the process running a stream hands it.
type Into struct {
	// Opened, when set, is told that somebody started reading.
	Opened func(path string, from node.ID)
}

// Stream serves the output of a command.
type Stream struct {
	into Into
}

func New(into Into) *Stream { return &Stream{into: into} }

func (s *Stream) Name() string { return "stream" }
func (s *Stream) Version() int { return 1 }

// Read takes the command a stream namespace runs.
func (s *Stream) Read(d arch.Declared) (arch.Config, error) {
	command, _ := d.String("command")
	if command == "" {
		return nil, fmt.Errorf("a stream namespace needs a command")
	}
	return Config{Command: command}, nil
}

func (s *Stream) Note(c arch.Config) arch.Note {
	cfg, _ := c.(Config)
	return arch.Note{
		Detail: cfg.Command,
		About:  "output from a command, as it comes",
		Glyph:  "▶",
	}
}

// Serve runs the command and streams what it writes until it ends or the far end goes.
func (s *Stream) Serve(ctx context.Context, at arch.Session) error {
	cfg, ok := at.Config.(Config)
	if !ok || cfg.Command == "" {
		return fmt.Errorf("%s has no command to run", at.Path)
	}
	if s.into.Opened != nil {
		s.into.Opened(at.Path, at.From)
	}

	d := live.New(at.Conn, at.Stream)

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cfg.Command)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s: %w", at.Path, err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("running %q: %w", cfg.Command, err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	done := make(chan error, 1)
	go func() { done <- d.Pump(io.Discard) }()

	// Nothing the far end sends is read into anything, so the session lasts exactly as long as the
	// command's output does. The read side is ended on the way out rather than waited on: a peer
	// that opened a stream and went quiet would otherwise hold this goroutine and the command with
	// it for as long as it kept the connection.
	defer func() {
		d.Stop()
		select {
		case <-done:
		case <-time.After(partingWithin):
		}
	}()

	// Bounded, because a far end that has stopped reading takes nothing at all, and the copy that
	// feeds it is what keeps the command running.
	writing := live.Pacing(asTerminal{d}, stalledAfter)
	defer writing.Give()

	if _, err := io.Copy(writing, out); err != nil {
		return err
	}
	return d.Close()
}

// asTerminal turns bare newlines into carriage return and newline.
//
// A command's output goes through a pipe, not a terminal, so nothing translates its line endings —
// the kernel does that for a pty, and a stream namespace has none. What arrives at the far end is
// drawn on a terminal screen, where a line feed moves down without moving back, and the result is
// each line starting further right than the last.
type asTerminal struct{ to io.Writer }

func (a asTerminal) Write(p []byte) (int, error) {
	// Only newlines that are not already part of a pair, so a command that does its own
	// translation is not given two carriage returns.
	var out []byte
	for i, b := range p {
		if b == '\n' && (i == 0 || p[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, b)
	}

	if _, err := a.to.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
