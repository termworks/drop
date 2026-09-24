package stream

import (
	"context"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/wire"
)

func TestStreamCommandsHaveAProcessWideLimit(t *testing.T) {
	processes := make(chan struct{}, 1)
	s := &Stream{processes: processes}
	processes <- struct{}{}

	err := s.Serve(context.Background(), arch.Session{
		Path:   "/log",
		Config: Config{Command: "echo refused"},
	})
	if err == nil || !strings.Contains(err.Error(), "1 stream commands") {
		t.Fatalf("the command above the process limit ended with %v", err)
	}

	<-processes
	for i := 0; i < 2; i++ {
		quiet := newQuiet()
		err := s.Serve(context.Background(), arch.Session{
			Path:   "/log",
			Config: Config{Command: "echo accepted"},
			Conn:   wire.NewConn(quiet),
			Stream: quiet,
		})
		if err != nil {
			t.Fatalf("command %d after capacity returned: %v", i, err)
		}
	}
}
