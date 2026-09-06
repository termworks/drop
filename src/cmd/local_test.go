package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalDialHonorsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.sock")
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if conn, err := dialLocal(canceled, path); !errors.Is(err, context.Canceled) {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("dial with canceled context returned %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	client, err := dialLocal(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	read := make(chan error, 1)
	go func() {
		var one [1]byte
		_, err := client.Read(one[:])
		read <- err
	}()
	cancel()

	select {
	case err := <-read:
		if err == nil {
			t.Fatal("a read ended without an error")
		}
	case <-time.After(time.Second):
		t.Fatal("a local connection outlived its context")
	}
}

func TestLocalDialPreservesHalfClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.sock")
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	client, err := dialLocal(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	if _, err := io.WriteString(client, "request"); err != nil {
		t.Fatal(err)
	}
	half, ok := client.(interface{ CloseWrite() error })
	if !ok {
		t.Fatal("a local Unix connection lost CloseWrite")
	}
	if err := half.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(server); err != nil || string(got) != "request" {
		t.Fatalf("server read %q, %v", got, err)
	}
	if _, err := server.Write([]byte("r")); err != nil {
		t.Fatal(err)
	}
	var reply [1]byte
	if _, err := io.ReadFull(client, reply[:]); err != nil || reply != [1]byte{'r'} {
		t.Fatalf("client read %q, %v", reply, err)
	}
}

type deadlineTrackingConn struct {
	net.Conn
	deadlines      []time.Time
	writeDeadlines []time.Time
	shortWrites    bool
}

func (c *deadlineTrackingConn) SetReadDeadline(deadline time.Time) error {
	c.deadlines = append(c.deadlines, deadline)
	return c.Conn.SetReadDeadline(deadline)
}

func (c *deadlineTrackingConn) SetWriteDeadline(deadline time.Time) error {
	c.writeDeadlines = append(c.writeDeadlines, deadline)
	if c.shortWrites && !deadline.IsZero() {
		deadline = time.Now().Add(20 * time.Millisecond)
	}
	return c.Conn.SetWriteDeadline(deadline)
}

// The pairing line between a local `drop pair` and the daemon is exactly three fields. Both ends
// are the same binary, so anything else is malformed rather than an older spelling to tolerate.
func TestAPairingOfferIsThreeFields(t *testing.T) {
	code, as, machine, err := offerAsked("abcd-efgh-ijkl bob person")
	if err != nil {
		t.Fatalf("a well-formed line was refused: %v", err)
	}
	if code != "abcd-efgh-ijkl" || as != "bob" || machine {
		t.Errorf("read %q, %q, %v", code, as, machine)
	}

	// A dash is a name that was not given, not a device called "-".
	if _, as, _, err = offerAsked("abcd-efgh-ijkl - machine"); err != nil || as != "" {
		t.Errorf("a dash came out as %q (%v)", as, err)
	}
	if _, _, machine, err = offerAsked("abcd-efgh-ijkl - machine"); err != nil || !machine {
		t.Errorf("machine came out %v (%v)", machine, err)
	}

	for _, bad := range []string{"", "abcd-efgh-ijkl", "abcd-efgh-ijkl bob"} {
		if _, _, _, err := offerAsked(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestLocalRequestLinesAreBounded(t *testing.T) {
	line := strings.Repeat("x", maxLocalLine-1) + "\n"
	if got, err := readLocalLine(bufio.NewReader(strings.NewReader(line))); err != nil || got != line {
		t.Fatalf("readLocalLine() = %d bytes, %v", len(got), err)
	}
	tooLong := strings.Repeat("x", maxLocalLine) + "\n"
	if _, err := readLocalLine(bufio.NewReader(strings.NewReader(tooLong))); err == nil {
		t.Fatal("readLocalLine() accepted an oversized request")
	}
}

func TestLocalRepliesHaveAHandshakeDeadline(t *testing.T) {
	client, server := net.Pipe()
	tracked := &deadlineTrackingConn{Conn: client}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	written := make(chan error, 1)
	go func() {
		_, err := server.Write([]byte("ok\n"))
		written <- err
	}()

	line, err := readLocalReply(tracked, bufio.NewReader(tracked))
	if err != nil || line != "ok\n" {
		t.Fatalf("readLocalReply() = %q, %v", line, err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if len(tracked.deadlines) != 2 || tracked.deadlines[0].IsZero() || !tracked.deadlines[1].IsZero() {
		t.Fatalf("read deadlines = %v, want one bounded deadline followed by a reset", tracked.deadlines)
	}
}

func TestLocalWritesHaveAHandshakeDeadline(t *testing.T) {
	client, server := net.Pipe()
	tracked := &deadlineTrackingConn{Conn: client}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	read := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(server).ReadString('\n')
		if err == nil && line != "ok\n" {
			err = fmt.Errorf("local write = %q", line)
		}
		read <- err
	}()

	if err := writeLocal(tracked, "ok\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	if len(tracked.writeDeadlines) != 2 || tracked.writeDeadlines[0].IsZero() || !tracked.writeDeadlines[1].IsZero() {
		t.Fatalf("write deadlines = %v, want one bounded deadline followed by a reset", tracked.writeDeadlines)
	}
}

func TestLocalWriteStopsWhenThePeerDoesNotRead(t *testing.T) {
	client, server := net.Pipe()
	tracked := &deadlineTrackingConn{Conn: client, shortWrites: true}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	started := time.Now()
	if err := writeLocal(tracked, "blocked\n"); err == nil {
		t.Fatal("a local write waited forever for a peer that reads nothing")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("a stalled local write took %s to stop", time.Since(started))
	}
	if len(tracked.writeDeadlines) != 2 || !tracked.writeDeadlines[1].IsZero() {
		t.Fatalf("write deadlines = %v, want a bound followed by a reset", tracked.writeDeadlines)
	}
}

func TestLocalRequestSurvivesDeadlineResetFailure(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	answered := make(chan error, 1)
	go func() {
		defer func() { _ = server.Close() }()
		if _, err := server.Write([]byte("held\n")); err != nil {
			answered <- err
			return
		}
		line, err := bufio.NewReader(server).ReadString('\n')
		if err == nil && line != heldReplyEnd+"\n" {
			err = fmt.Errorf("held reply = %q", line)
		}
		answered <- err
	}()

	if err := takeLocal(t.Context(), nil, nil, nil, nil, nil, resetFailConn{client}); err != nil {
		t.Fatalf("a complete local request was discarded: %v", err)
	}
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
}

func TestOnlyOneLocalServerOwnsTheSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.sock")
	first, err := localGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := localGuard(path); err == nil {
		_ = second.Close()
		t.Fatal("a second local server acquired the socket lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := localGuard(path)
	if err != nil {
		t.Fatalf("the socket lock stayed held: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	if stat, err := os.Stat(path + ".lock"); err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatalf("socket lock mode = %v, %v", stat, err)
	}
}

func TestLocalServerOwnsAndCleansUpItsSocket(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	runtime, err := os.MkdirTemp("/tmp", "drop-local-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtime) })
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	server, err := openLocalServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(server.path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 || stat.Mode()&os.ModeSocket == 0 {
		t.Fatalf("local socket mode = %v", stat.Mode())
	}
	if second, err := openLocalServer(ctx); err == nil {
		_ = second.Close()
		t.Fatal("a second local server opened the same socket")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("closing twice: %v", err)
	}
	if _, err := os.Stat(server.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closed server left its socket: %v", err)
	}

	again, err := openLocalServer(ctx)
	if err != nil {
		t.Fatalf("reopening after close: %v", err)
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		_, statErr := os.Stat(again.path)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("canceled server left its socket: %v", statErr)
		}
		time.Sleep(time.Millisecond)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServeRefusesAnUnavailableLocalSocket(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	runtime, err := os.MkdirTemp("/tmp", "drop-local-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtime) })
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv("DROP_PORT", "0")
	if err := os.MkdirAll(filepath.Join(config, "drop"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "local drop = require(\"drop\")\ndrop.mount(\"/chat\", { type = \"chat\" })\n"
	if err := os.WriteFile(filepath.Join(config, "drop", "init.lua"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := castSocket()
	if err != nil {
		t.Fatal(err)
	}
	guard, err := localGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = guard.Close() }()

	err = runServe(t.Context(), true)
	if err == nil || !strings.Contains(err.Error(), "starting the local control socket") {
		t.Fatalf("runServe() = %v", err)
	}
}

func TestAcceptFailuresBackOffToTheLimit(t *testing.T) {
	waiting := time.Duration(0)
	want := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
		160 * time.Millisecond,
		320 * time.Millisecond,
		640 * time.Millisecond,
		1280 * time.Millisecond,
		2 * time.Second,
		2 * time.Second,
	}
	for attempt, expected := range want {
		waiting = nextAcceptWait(waiting)
		if waiting != expected {
			t.Fatalf("attempt %d waits %s, want %s", attempt+1, waiting, expected)
		}
	}
}
