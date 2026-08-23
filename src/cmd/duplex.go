package cmd

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// serveDuplex answers a live stream according to what the namespace says it is.
func serveDuplex(pinned *book.Book, shells *terminals) func(proto.Resolved, *proto.Duplex) error {
	return func(at proto.Resolved, d *proto.Duplex) error {
		who := nameFor(pinned, at.From)

		switch at.Mount.Kind {
		case ns.KindStream:
			fmt.Printf("  %s opened %s\n", who, at.Mount.Path)
			return pipeCommand(at, d)
		case ns.KindTTY:
			live, err := shells.at(at.Mount)
			if err != nil {
				return err
			}
			fmt.Printf("  %s is watching %s (%d total)\n", who, at.Mount.Path, live.stage.Watching()+1)
			return serveTTY(at, d, live)
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
