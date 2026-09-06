package cmd

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
)

func TestViaDaemonKeepsBytesBufferedAfterItsReply(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	go func() {
		defer func() { _ = server.Close() }()
		_, _ = server.Write([]byte("ok\npayload"))
	}()

	stream, err := acceptLent(client, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("borrowed stream = %q", got)
	}
}

func TestAConnectedDaemonFailureDoesNotBecomeNoDaemon(t *testing.T) {
	server, client := net.Pipe()
	_ = server.Close()

	if _, err := acceptLent(client, "alpha"); err == nil {
		t.Fatal("a daemon that closed without answering was accepted")
	} else if errors.Is(err, errNoDaemon) {
		t.Fatalf("a connected daemon failure became %v", errNoDaemon)
	}
}

type trackedConn struct {
	net.Conn
	half atomic.Bool
	full atomic.Bool
}

func (c *trackedConn) CloseWrite() error {
	c.half.Store(true)
	return nil
}

func (c *trackedConn) Close() error {
	c.full.Store(true)
	return c.Conn.Close()
}

func TestBorrowedStreamAndSocketCloseSeparately(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	tracked := &trackedConn{Conn: client}
	stream := &lent{Conn: tracked, read: tracked}
	done := lentDone{stream}

	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if !tracked.half.Load() || tracked.full.Load() {
		t.Fatalf("stream close: half=%v full=%v", tracked.half.Load(), tracked.full.Load())
	}
	if err := done.Close(); err != nil {
		t.Fatal(err)
	}
	if !tracked.full.Load() {
		t.Fatal("borrowed socket stayed open after its owner closed")
	}
}
