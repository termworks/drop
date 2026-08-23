package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/creack/pty"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// serveDuplex answers a live stream according to what the namespace says it is.
func serveDuplex(pinned *book.Book) func(proto.Resolved, *proto.Duplex) error {
	return func(at proto.Resolved, d *proto.Duplex) error {
		who := nameFor(pinned, at.From)

		switch at.Mount.Kind {
		case ns.KindStream:
			fmt.Printf("  %s opened %s\n", who, at.Mount.Path)
			return pipeCommand(at, d)
		case ns.KindTTY:
			fmt.Printf("  %s opened %s\n", who, at.Mount.Path)
			return serveTTY(at, d)
		default:
			return fmt.Errorf("%s is not a live namespace", at.Mount.Path)
		}
	}
}

// pipeCommand runs the namespace's command and streams what it writes. Nothing knows how long it
// will run, which is the point of a stream namespace.
func pipeCommand(at proto.Resolved, d *proto.Duplex) error {
	cmd := exec.Command("/bin/sh", "-c", at.Mount.Command)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s: %w", at.Mount.Path, err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("running %q: %w", at.Mount.Command, err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	done := make(chan error, 1)
	go func() { done <- d.Pump(io.Discard) }()

	if _, err := io.Copy(d, out); err != nil {
		return err
	}
	_ = d.Close()
	return <-done
}

// serveTTY starts a shell in a pty for one caller. Input reaches it only when the namespace said
// it may.
func serveTTY(at proto.Resolved, d *proto.Duplex) error {
	shell := at.Mount.Shell
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("%s: %w", at.Mount.Path, err)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: 80, Rows: 24})
	d.OnResize = func(cols, rows uint16) {
		_ = pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
	}

	into := io.Writer(io.Discard)
	if at.Mount.Input {
		into = ptmx
	}

	done := make(chan error, 1)
	go func() { done <- d.Pump(into) }()

	if _, err := io.Copy(d, ptmx); err != nil {
		return err
	}
	_ = d.Close()
	return <-done
}
