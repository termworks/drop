package proto

import (
	"net"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/wire"
)

func TestAskRefusesAFrameThatIsNotAnAnswer(t *testing.T) {
	caller, server := net.Pipe()
	t.Cleanup(func() { _ = caller.Close() })

	go func() {
		defer func() { _ = server.Close() }()
		conn := wire.NewConn(server)
		if _, _, err := conn.ReadFrame(); err != nil {
			return
		}
		_ = conn.WriteFrame(wire.KindItem, nil)
	}()

	err := Ask(t.Context(), caller, "/notes", "please", "tester")
	if err == nil || !strings.Contains(err.Error(), "expected an answer") {
		t.Fatalf("Ask() accepted a non-answer frame: %v", err)
	}
}
