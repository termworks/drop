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
	deadlines []time.Time
}

func (c *deadlineTrackingConn) SetReadDeadline(deadline time.Time) error {
	c.deadlines = append(c.deadlines, deadline)
	return c.Conn.SetReadDeadline(deadline)
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
