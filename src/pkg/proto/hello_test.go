package proto

import (
	"bytes"
	"fmt"
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

func (pipeEnd) Close() error                     { return nil }
func (pipeEnd) SetReadDeadline(time.Time) error  { return nil }
func (pipeEnd) SetWriteDeadline(time.Time) error { return nil }

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

func (d *deadlined) SetWriteDeadline(time.Time) error { return nil }

type writeDeadlined struct {
	read io.Reader
	set  chan struct{}
	once sync.Once
}

func (d *writeDeadlined) Read(p []byte) (int, error) { return d.read.Read(p) }
func (d *writeDeadlined) Write([]byte) (int, error) {
	<-d.set
	return 0, os.ErrDeadlineExceeded
}
func (d *writeDeadlined) Close() error {
	d.once.Do(func() { close(d.set) })
	return nil
}
func (d *writeDeadlined) SetReadDeadline(time.Time) error { return nil }
func (d *writeDeadlined) SetWriteDeadline(at time.Time) error {
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
		done <- AnswerHello(silent, node.ID{}, func(Badged) Hello { return Hello{Name: "beta"} }, nil)
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

func TestAHelloAnswerThatCannotBeWrittenIsNotHeldForever(t *testing.T) {
	blocked := &writeDeadlined{
		read: bytes.NewReader([]byte{wire.KindPing, 0}),
		set:  make(chan struct{}),
	}
	t.Cleanup(func() { _ = blocked.Close() })

	done := make(chan error, 1)
	go func() {
		done <- AnswerHello(blocked, node.ID{}, func(Badged) Hello { return Hello{Name: "beta"} }, nil)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a blocked hello answer was written")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AnswerHello is still writing to a peer that reads nothing")
	}
}

func TestAHelloAskThatCannotBeWrittenIsNotHeldForever(t *testing.T) {
	blocked := &writeDeadlined{read: &bytes.Buffer{}, set: make(chan struct{})}
	t.Cleanup(func() { _ = blocked.Close() })

	done := make(chan error, 1)
	go func() {
		_, err := AskHello(blocked)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a blocked hello ask was written")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AskHello is still writing to a peer that reads nothing")
	}
}

func TestHelloRefusesWrongFrameKinds(t *testing.T) {
	t.Run("ask", func(t *testing.T) {
		var framed bytes.Buffer
		if err := wire.NewConn(&framed).WriteFrame(wire.KindItem, showable()); err != nil {
			t.Fatal(err)
		}
		stream := pipeEnd{Reader: &framed, Writer: io.Discard}
		if err := AnswerHello(stream, node.ID{}, func(Badged) Hello { return Hello{} }, nil); err == nil {
			t.Fatal("AnswerHello() accepted a non-ask frame")
		}
	})

	t.Run("answer", func(t *testing.T) {
		var framed bytes.Buffer
		if err := wire.NewConn(&framed).WriteFrame(wire.KindItem, Hello{}.encode()); err != nil {
			t.Fatal(err)
		}
		if _, err := readHello(wire.NewConn(&framed)); err == nil {
			t.Fatal("readHello() accepted a non-answer frame")
		}
	})
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
		done <- AnswerHello(server, node.ID{}, func(Badged) Hello { return want }, nil)
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

// A node never says more than its own reader will take. The reader refuses an over-long list for
// the whole message, so a node with one crowded namespace would otherwise be a node nobody can list
// at all — including on the namespaces that have nothing to do with it.
func TestHelloCutsALongHolderListRatherThanBecomingUnreadable(t *testing.T) {
	crowd := make([]string, MaxHolders+1)
	for i := range crowd {
		crowd[i] = fmt.Sprintf("ssh-ed25519 AAAA%04d somebody\n", i)
	}

	said := Hello{
		Name:    "laptop",
		Version: "0.1.0",
		Serves: []Served{
			{Path: "/files", Archetype: "files"},
			{
				Path:      "/notes",
				Archetype: "chat",
				Shared:    ns.Shared{Creator: crowd[0], At: "/notes", Nonce: "cafe"},
				Holders:   crowd,
			},
		},
	}

	got, err := decodeHello(said.encode())
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.Serves) != 2 {
		t.Fatalf("%d namespaces came back, want 2", len(got.Serves))
	}
	if len(got.Serves[1].Holders) != MaxHolders {
		t.Fatalf("%d holders came back, want %d", len(got.Serves[1].Holders), MaxHolders)
	}
}

// The same for the namespaces themselves.
func TestHelloCutsALongNamespaceListRatherThanBecomingUnreadable(t *testing.T) {
	said := Hello{Name: "laptop", Version: "0.1.0"}
	for i := range MaxServed + 10 {
		said.Serves = append(said.Serves, Served{Path: fmt.Sprintf("/p%d", i), Archetype: "chat"})
	}

	got, err := decodeHello(said.encode())
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.Serves) != MaxServed {
		t.Fatalf("%d namespaces came back, want %d", len(got.Serves), MaxServed)
	}
}

// Marks travel, and a hello with none is written exactly as one from before marks existed, so a
// machine that predates them still reads it.
func TestAHelloCarriesMarksOnlyWhenThereAreSome(t *testing.T) {
	plain := Hello{Name: "tron", Version: "0.5.1", Circle: []byte("c"), Mine: []Member{{ID: "a", Name: "phone"}}}
	marked := plain
	marked.Gone = []Mark{{ID: "a", At: 1700000000, Gone: true}, {ID: "b", At: 5}}

	got, err := decodeHello(marked.encode())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Gone) != 2 || got.Gone[0] != marked.Gone[0] || got.Gone[1] != marked.Gone[1] {
		t.Fatalf("marks came back as %+v", got.Gone)
	}

	before := plain.encode()
	if back, err := decodeHello(before); err != nil || len(back.Gone) != 0 {
		t.Fatalf("a hello without marks read as %+v (%v)", back.Gone, err)
	}
	if len(before) >= len(marked.encode()) {
		t.Fatal("a hello without marks is not the shorter one")
	}
}
