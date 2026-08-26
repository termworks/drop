package proto

import (
	"io"
	"testing"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

func TestHelloRoundTrip(t *testing.T) {
	want := Hello{
		Name:    "laptop",
		Version: "0.1.0",
		Serves: []Served{
			{Path: "/inbox", Archetype: ns.Share, Writable: true},
			{Path: "/term", Archetype: ns.TTY, Writable: false},
			{Path: "/logs", Archetype: ns.Stream, Writable: false},
		},
	}

	got, err := decodeHello(want.encode())
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Name != want.Name || got.Version != want.Version {
		t.Fatalf("got %+v", got)
	}
	if len(got.Serves) != len(want.Serves) {
		t.Fatalf("serves = %+v", got.Serves)
	}
	for i, s := range want.Serves {
		if got.Serves[i] != s {
			t.Fatalf("serves[%d] = %+v, want %+v", i, got.Serves[i], s)
		}
	}
}

// A hello with nothing to say about namespaces has to decode, because that is what a node answers
// a caller that may reach nothing at all.
func TestHelloWithoutNamespaces(t *testing.T) {
	got, err := decodeHello(Hello{Name: "laptop", Version: "0.1.0"}.encode())
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.Serves) != 0 {
		t.Fatalf("serves = %+v", got.Serves)
	}
}

// The count is read off the wire, so a peer claiming a huge one must be refused rather than
// believed: it is another machine's number, not ours.
func TestHelloRefusesTooManyNamespaces(t *testing.T) {
	body := func() []byte {
		w := newWriterFor("laptop", "0.1.0")
		w.Uint(uint64(MaxServed) + 1)
		return w.Body()
	}()

	if _, err := decodeHello(body); err == nil {
		t.Fatal("an absurd namespace count was accepted")
	}
}

// A truncated list must be an error rather than a short one silently accepted.
func TestHelloRefusesATruncatedList(t *testing.T) {
	w := newWriterFor("laptop", "0.1.0")
	w.Uint(3)
	w.String("/inbox")
	w.Byte(byte(ns.Share))
	w.Bool(true)

	if _, err := decodeHello(w.Body()); err == nil {
		t.Fatal("a list shorter than its own count was accepted")
	}
}

// newWriterFor starts a hello body, so a test can hand-build a malformed tail.
func newWriterFor(name, version string) *wire.Writer {
	w := wire.NewWriter()
	w.String(name)
	w.String(version)
	return w
}

// pipeEnds gives two halves of a connection, so the two sides of a hello can be run against each
// other rather than against an assumption about who speaks first.
type pipeEnd struct {
	io.Reader
	io.Writer
}

func (pipeEnd) Close() error { return nil }

// The client speaks first and the server answers. Getting this backwards deadlocks on a real QUIC
// stream, because a stream the client never wrote to is never handed to the server at all.
func TestHelloIsAskedThenAnswered(t *testing.T) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()

	client := pipeEnd{Reader: clientR, Writer: clientW}
	server := pipeEnd{Reader: serverR, Writer: serverW}

	want := Hello{Name: "beta", Version: "0.1.0", Serves: []Served{{Path: "/tty", Archetype: ns.TTY}}}

	done := make(chan error, 1)
	go func() {
		done <- AnswerHello(server, node.ID{}, func(Badged) Hello { return want })
	}()

	got, err := AskHello(client)
	if err != nil {
		t.Fatalf("asking: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("answering: %v", err)
	}

	if got.Name != want.Name || len(got.Serves) != 1 || got.Serves[0].Path != "/tty" {
		t.Fatalf("got %+v", got)
	}
}
