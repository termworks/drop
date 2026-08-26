package proto

import (
	"io"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

func TestHelloRoundTrip(t *testing.T) {
	want := Hello{
		Name:    "laptop",
		Version: "0.1.0",
		Serves: []Served{
			{Path: "/inbox", Archetype: "share", Version: 1, Writable: true, About: "hand files over, once"},
			{Path: "/term", Archetype: "tty", Version: 1},
			{Path: "/logs", Archetype: "stream", Version: 2, About: "output from a command"},
			{
				Path:      "/notes",
				Archetype: "chat",
				Version:   1,
				Writable:  true,
				Shared:    ns.Shared{Creator: "ssh-ed25519 AAAA alice\n", At: "/notes", Nonce: "cafe"},
				Holders:   []string{"ssh-ed25519 AAAA alice\n", "ssh-ed25519 BBBB bob\n"},
			},
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
		if !reflect.DeepEqual(got.Serves[i], s) {
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
	w.String("share")
	w.Uint(1)
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

func (pipeEnd) Close() error                    { return nil }
func (pipeEnd) SetReadDeadline(time.Time) error { return nil }

// deadlined is a stream that will never say anything, and unblocks only when a read deadline is set
// on it — which is what a real one does to a peer that sent nothing.
type deadlined struct {
	set  chan struct{}
	once sync.Once
}

func (d *deadlined) Read([]byte) (int, error)    { <-d.set; return 0, os.ErrDeadlineExceeded }
func (d *deadlined) Write(p []byte) (int, error) { return len(p), nil }
func (d *deadlined) Close() error                { return nil }

func (d *deadlined) SetReadDeadline(at time.Time) error {
	if !at.IsZero() {
		d.once.Do(func() { close(d.set) })
	}
	return nil
}

// A hello is answered to anybody who dials, so a stranger who opens a stream, writes nothing and
// stays connected must not hold a goroutine and its buffers for the life of the daemon.
func TestAHelloThatSaysNothingIsNotHeldForever(t *testing.T) {
	silent := &deadlined{set: make(chan struct{})}

	done := make(chan error, 1)
	go func() {
		done <- AnswerHello(silent, node.ID{}, func(Badged) Hello { return Hello{Name: "beta"} })
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a hello that said nothing was answered")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AnswerHello is still reading a stream that will never say anything")
	}
}

// The client speaks first and the server answers. Getting this backwards deadlocks on a real QUIC
// stream, because a stream the client never wrote to is never handed to the server at all.
func TestHelloIsAskedThenAnswered(t *testing.T) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()

	client := pipeEnd{Reader: clientR, Writer: clientW}
	server := pipeEnd{Reader: serverR, Writer: serverW}

	want := Hello{Name: "beta", Version: "0.1.0", Serves: []Served{{Path: "/tty", Archetype: "tty"}}}

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
