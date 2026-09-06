package cmd

import (
	"io"
	"net"
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
