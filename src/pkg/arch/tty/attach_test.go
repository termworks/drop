package tty

import (
	"testing"
	"time"
)

// A watcher that stops reading takes nothing, and the write to it never returns. The shell behind
// it is shared, so that watcher is given up on.
func TestAWatcherThatStopsReadingIsDropped(t *testing.T) {
	stuck := &stopped{let: make(chan struct{})}
	defer close(stuck.let)

	writing := pacing(stuck)
	if err := writing.write([]byte("anything"), 50*time.Millisecond); err != errStalled {
		t.Fatalf("a write that went nowhere came back with %v", err)
	}
}

// And one that is being read lands, in order.
func TestAWatcherThatReadsIsWrittenTo(t *testing.T) {
	var got taken

	writing := pacing(&got)
	for _, chunk := range []string{"one ", "two ", "three"} {
		if err := writing.write([]byte(chunk), time.Second); err != nil {
			t.Fatalf("writing %q: %v", chunk, err)
		}
	}

	if string(got.got) != "one two three" {
		t.Fatalf("the watcher saw %q", got.got)
	}
}

// taken keeps whatever was written to it.
type taken struct{ got []byte }

func (t *taken) Write(p []byte) (int, error) {
	t.got = append(t.got, p...)
	return len(p), nil
}

// stopped takes nothing until it is let go, which is what a window that stopped reading looks like
// from this end.
type stopped struct{ let chan struct{} }

func (s *stopped) Write(p []byte) (int, error) {
	<-s.let
	return len(p), nil
}
