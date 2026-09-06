package note

import (
	"bytes"
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/wire"
)

func TestAStoppedNoteWatcherSaysItIsFinished(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := New(Into{}).Watch(ctx, nil)

	select {
	case <-done:
		t.Fatal("the watcher stopped before its context")
	default:
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the watcher did not finish after its context stopped")
	}
}

func TestANoteNeedsAFileAndSaysSo(t *testing.T) {
	n := New(Into{})

	if _, err := n.Read(made.Declared(made.Settings{})); err == nil {
		t.Fatal("a note with no file was read")
	} else if !strings.Contains(err.Error(), "file") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}

	cfg, err := n.Read(made.Declared(made.Settings{"file": "/tmp/notes.md"}))
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if got := cfg.(Config).File; got != "/tmp/notes.md" {
		t.Errorf("the file is %q", got)
	}
	if note := n.Note(cfg); !note.Shareable || note.Detail != "/tmp/notes.md" {
		t.Errorf("a note says %+v about itself", note)
	}
}

// Whether several machines may hold one is asked before a declaration has been read, so it cannot
// depend on one.
func TestANoteIsSomethingSeveralMachinesHoldBeforeAnybodyDeclaresOne(t *testing.T) {
	if !New(Into{}).Note(nil).Shareable {
		t.Fatal("a note with nothing declared says it is one machine's own")
	}
}

func TestANoteIsSentWithACompleteEnding(t *testing.T) {
	file := t.TempDir() + "/note.txt"
	if err := os.WriteFile(file, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	served := make(chan error, 1)
	go func() {
		served <- New(Into{}).Serve(t.Context(), arch.Session{
			Config: Config{File: file},
			Conn:   wire.NewConn(server),
		})
	}()

	conn := wire.NewConn(client)
	kind, body, err := conn.ReadFrame()
	if err != nil || kind != wire.KindItem || string(body) != "hello\n" {
		t.Fatalf("note = kind %d, %q, %v", kind, body, err)
	}
	kind, body, err = conn.ReadFrame()
	if err != nil || kind != wire.KindEnd {
		t.Fatalf("ending = kind %d, %q, %v", kind, body, err)
	}
	end, err := wire.DecodeEnd(body)
	if err != nil || end.Size != int64(len("hello\n")) {
		t.Fatalf("ending = %+v, %v", end, err)
	}
	if err := <-served; err != nil {
		t.Fatalf("Serve() = %v", err)
	}
}

func TestASingleItemNoteIsAccepted(t *testing.T) {
	var framed bytes.Buffer
	if err := wire.NewConn(&framed).WriteFrame(wire.KindItem, []byte("camera")); err != nil {
		t.Fatal(err)
	}
	body, err := Text(wire.NewConn(&framed))
	if err != nil || string(body) != "camera" {
		t.Fatalf("Text() = %q, %v", body, err)
	}
}

func TestATruncatedNoteFrameIsRefused(t *testing.T) {
	framed := bytes.NewBuffer([]byte{wire.KindItem, 4, 'a'})
	if _, err := Text(wire.NewConn(framed)); err == nil {
		t.Fatal("Text() accepted a truncated frame")
	}
}

func TestAnOversizedNoteIsRefusedBeforeItIsAllocated(t *testing.T) {
	var framed bytes.Buffer
	if err := wire.NewConn(&framed).WriteFrame(wire.KindItem, make([]byte, MaxSize+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := Text(wire.NewConn(&framed)); err == nil {
		t.Fatal("Text() accepted an oversized note")
	}
}

func TestAStalledNoteReadStops(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	started := time.Now()
	if _, err := Text(wire.NewConn(shortDeadline{Conn: client})); err == nil {
		t.Fatal("Text() waited forever for a note")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("Text() took %s to stop", time.Since(started))
	}
}

func TestAStalledNoteWriteStops(t *testing.T) {
	file := t.TempDir() + "/note.txt"
	if err := os.WriteFile(file, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	started := time.Now()
	err := New(Into{}).Serve(t.Context(), arch.Session{
		Config: Config{File: file},
		Conn:   wire.NewConn(shortDeadline{Conn: server}),
	})
	if err == nil {
		t.Fatal("Serve() waited forever to write a note")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("Serve() took %s to stop", time.Since(started))
	}
}

type shortDeadline struct{ net.Conn }

func (s shortDeadline) SetReadDeadline(at time.Time) error {
	if !at.IsZero() {
		at = time.Now().Add(20 * time.Millisecond)
	}
	return s.Conn.SetReadDeadline(at)
}

func (s shortDeadline) SetWriteDeadline(at time.Time) error {
	if !at.IsZero() {
		at = time.Now().Add(20 * time.Millisecond)
	}
	return s.Conn.SetWriteDeadline(at)
}
