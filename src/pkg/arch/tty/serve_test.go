package tty

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/live"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

type terminalStream struct {
	input *bytes.Reader
	mu    sync.Mutex
	out   bytes.Buffer
}

func terminalFrames(t *testing.T, send func(*live.Duplex)) *terminalStream {
	t.Helper()
	var framed bytes.Buffer
	sender := live.New(wire.NewConn(&framed), nil)
	send(sender)
	return &terminalStream{input: bytes.NewReader(framed.Bytes())}
}

func (s *terminalStream) Read(p []byte) (int, error) { return s.input.Read(p) }

func (s *terminalStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.Write(p)
}

func (s *terminalStream) Close() error                     { return nil }
func (s *terminalStream) SetReadDeadline(time.Time) error  { return nil }
func (s *terminalStream) SetWriteDeadline(time.Time) error { return nil }
func (s *terminalStream) output() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.out.Bytes()...)
}

func terminalScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func serveTerminal(t *testing.T, tty *TTY, stream *terminalStream, cfg Config, from node.ID) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- tty.Serve(context.Background(), arch.Session{
			Path:   "/terminal",
			Config: cfg,
			From:   from,
			Conn:   wire.NewConn(stream),
			Stream: stream,
		})
	}()
	return done
}

func waitTerminal(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve(): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("terminal session did not end")
	}
}

func TestServeCarriesInputResizeAndShellExit(t *testing.T) {
	script := terminalScript(t, "IFS= read -r line\nset -- $(stty size)\nprintf 'received:%s size:%sx%s\\n' \"$line\" \"$2\" \"$1\"\n")
	stream := terminalFrames(t, func(sender *live.Duplex) {
		if err := sender.Resize(120, 40); err != nil {
			t.Fatal(err)
		}
		if _, err := sender.Write([]byte("hello\n")); err != nil {
			t.Fatal(err)
		}
		if err := sender.Close(); err != nil {
			t.Fatal(err)
		}
	})
	from := node.From([32]byte{1})
	var watchedPath string
	var watchedFrom node.ID
	var watching int
	terminals := New(Into{Watched: func(path string, peer node.ID, total int) {
		watchedPath, watchedFrom, watching = path, peer, total
	}})
	terminals.terminals = make(chan struct{}, 1)

	waitTerminal(t, serveTerminal(t, terminals, stream, Config{Shell: script, Input: true}, from))
	out := string(stream.output())
	if !strings.Contains(out, "received:hello") || !strings.Contains(out, "size:120x40") {
		t.Fatalf("terminal output = %q", out)
	}
	if watchedPath != "/terminal" || watchedFrom != from || watching != 1 {
		t.Fatalf("watcher report = %q, %s, %d", watchedPath, node.Brief(watchedFrom), watching)
	}
	terminals.mu.Lock()
	open := len(terminals.open)
	terminals.mu.Unlock()
	if open != 0 || len(terminals.terminals) != 0 {
		t.Fatalf("ended terminal retained %d sessions and %d slots", open, len(terminals.terminals))
	}
}

func TestServeWithholdsInputFromAReadOnlyTerminal(t *testing.T) {
	script := terminalScript(t, "printf 'ready\\n'\nIFS= read -r line\nprintf 'received:%s\\n' \"$line\"\n")
	stream := terminalFrames(t, func(sender *live.Duplex) {
		if _, err := sender.Write([]byte("forbidden\n")); err != nil {
			t.Fatal(err)
		}
		if err := sender.Close(); err != nil {
			t.Fatal(err)
		}
	})
	terminals := New(Into{})
	terminals.terminals = make(chan struct{}, 1)
	done := serveTerminal(t, terminals, stream, Config{Shell: script}, node.ID{})

	until := time.Now().Add(5 * time.Second)
	for !bytes.Contains(stream.output(), []byte("ready")) && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if !bytes.Contains(stream.output(), []byte("ready")) {
		t.Fatal("read-only terminal did not start")
	}
	if bytes.Contains(stream.output(), []byte("received:forbidden")) {
		t.Fatal("read-only terminal accepted input")
	}
	terminals.Stop()
	waitTerminal(t, done)
	if bytes.Contains(stream.output(), []byte("received:forbidden")) {
		t.Fatal("read-only terminal received input while stopping")
	}
}

func TestCancelledServeLeavesTheSharedShellRunning(t *testing.T) {
	script := terminalScript(t, "while :; do sleep 1; done\n")
	stream := newQuiet()
	terminals := New(Into{})
	terminals.terminals = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- terminals.Serve(ctx, arch.Session{
			Path:   "/terminal",
			Config: Config{Shell: script},
			Conn:   wire.NewConn(stream),
			Stream: stream,
		})
	}()

	var term *terminal
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		terminals.mu.Lock()
		term = terminals.open["/terminal"]
		terminals.mu.Unlock()
		if term != nil && term.stage.Watching() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if term == nil || term.stage.Watching() != 1 {
		t.Fatal("terminal watcher did not attach")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Serve() = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled terminal session did not end")
	}
	if term.stage.Watching() != 0 {
		t.Fatal("cancelled watcher remained attached")
	}
	select {
	case <-term.reaped:
		t.Fatal("one cancelled watcher ended the shared shell")
	default:
	}
	terminals.Stop()
}

func TestServeAttachesToAnExistingCast(t *testing.T) {
	stage := cast.New(80, 24)
	_, _ = stage.Write([]byte("already here"))
	stream := terminalFrames(t, func(sender *live.Duplex) {
		if err := sender.Close(); err != nil {
			t.Fatal(err)
		}
	})
	terminals := New(Into{Showing: func(path string) (*cast.Caster, bool) {
		return stage, path == "/terminal"
	}})
	done := serveTerminal(t, terminals, stream, Config{}, node.ID{})

	until := time.Now().Add(5 * time.Second)
	for stage.Watching() != 1 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if stage.Watching() != 1 {
		t.Fatal("cast was not joined")
	}
	_, _ = stage.Write([]byte("and now"))
	stage.Stop()
	waitTerminal(t, done)
	out := string(stream.output())
	if !strings.Contains(out, "already here") || !strings.Contains(out, "and now") {
		t.Fatalf("cast output = %q", out)
	}
	terminals.mu.Lock()
	open := len(terminals.open)
	terminals.mu.Unlock()
	if open != 0 {
		t.Fatalf("attaching to a cast started %d shells", open)
	}
}

func TestServeRefusesAnUnavailableCast(t *testing.T) {
	terminals := New(Into{Showing: func(string) (*cast.Caster, bool) { return nil, true }})
	err := terminals.Serve(t.Context(), arch.Session{Path: "/terminal"})
	if err == nil || !strings.Contains(err.Error(), "nothing is being cast") {
		t.Fatalf("unavailable cast = %v", err)
	}
}

type terminalDeclaration struct {
	shell string
	input bool
}

func (d terminalDeclaration) String(key string) (string, bool) { return d.shell, key == "shell" }
func (d terminalDeclaration) Bool(key string) (bool, bool)     { return d.input, key == "input" }
func (terminalDeclaration) Strings(string) ([]string, bool)    { return nil, false }

func TestTTYConfigurationAndNote(t *testing.T) {
	typeOf := New(Into{})
	if typeOf.Name() != "tty" || typeOf.Version() != 1 {
		t.Fatalf("archetype = %s/%d", typeOf.Name(), typeOf.Version())
	}
	read, err := typeOf.Read(terminalDeclaration{shell: "/bin/ksh", input: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := read.(Config)
	if !ok || cfg.Shell != "/bin/ksh" || !cfg.Input {
		t.Fatalf("config = %#v", read)
	}
	note := typeOf.Note(cfg)
	if !note.Writable || note.Detail != "interactive" || note.Glyph != "▮" {
		t.Fatalf("note = %#v", note)
	}
	readOnly := typeOf.Note(Config{})
	if readOnly.Writable || readOnly.Detail != "read-only" {
		t.Fatalf("read-only note = %#v", readOnly)
	}
}
