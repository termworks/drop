package live

import (
	"testing"
	"time"
)

// A far end that stops reading takes nothing, and the write to it never returns. What is behind it
// is shared with everybody else on it, so that end is given up on.
func TestAFarEndThatStopsReadingIsDropped(t *testing.T) {
	stuck := &stopped{let: make(chan struct{})}
	defer close(stuck.let)

	writing := Pacing(stuck, 50*time.Millisecond)
	if _, err := writing.Write([]byte("anything")); err != ErrStalled {
		t.Fatalf("a write that went nowhere came back with %v", err)
	}
	if _, err := writing.Write([]byte("more")); err != ErrStalled {
		t.Fatalf("a write after the stall came back with %v", err)
	}
}

// And one that is being read lands, in order.
func TestAFarEndThatReadsIsWrittenTo(t *testing.T) {
	var got taken

	writing := Pacing(&got, time.Second)
	defer writing.Give()

	for _, chunk := range []string{"one ", "two ", "three"} {
		if _, err := writing.Write([]byte(chunk)); err != nil {
			t.Fatalf("writing %q: %v", chunk, err)
		}
	}

	if string(got.got) != "one two three" {
		t.Fatalf("the far end saw %q", got.got)
	}
}

// taken keeps whatever was written to it.
type taken struct{ got []byte }

func (t *taken) Write(p []byte) (int, error) {
	t.got = append(t.got, p...)
	return len(p), nil
}

// stopped takes nothing until it is let go, which is what a peer that stopped reading looks like
// from this end.
type stopped struct{ let chan struct{} }

func (s *stopped) Write(p []byte) (int, error) {
	<-s.let
	return len(p), nil
}
