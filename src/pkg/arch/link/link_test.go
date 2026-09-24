package link

import (
	"bytes"
	"io"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

type declared map[string]string

func (d declared) String(key string) (string, bool) { value, ok := d[key]; return value, ok }
func (declared) Bool(string) (bool, bool)           { return false, false }
func (declared) Strings(string) ([]string, bool)    { return nil, false }

type readWriter struct {
	io.Reader
	io.Writer
}

func TestLinkReadsAndDescribesItsAction(t *testing.T) {
	l := New(Into{})
	cfg, err := l.Read(declared{"action": "xdg-open"})
	if err != nil {
		t.Fatal(err)
	}
	note := l.Note(cfg)
	if note.Detail != "xdg-open" || !note.Writable || note.Glyph != "◈" {
		t.Fatalf("Note() = %+v", note)
	}

	quiet := l.Note(Config{})
	if quiet.Detail != "recorded, not opened" {
		t.Fatalf("Note(Config{}) = %+v", quiet)
	}
}

func TestLinkStoresAndAcknowledgesAnArrival(t *testing.T) {
	message := convo.Message{ID: "link-one", Kind: convo.KindLink, Body: "https://example.com"}
	var in bytes.Buffer
	written := wire.NewConn(readWriter{&in, &in})
	if err := written.WriteFrame(wire.KindItem, message.Encode()); err != nil {
		t.Fatal(err)
	}
	if err := written.WriteFrame(wire.KindEnd, wire.End{Size: 1}.Encode()); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var got convo.Message
	l := New(Into{Store: func(_ node.ID, m convo.Message) error { got = m; return nil }})
	at := arch.Session{Conn: wire.NewConn(readWriter{&in, &out})}
	if err := l.Serve(t.Context(), at); err != nil {
		t.Fatalf("Serve(): %v", err)
	}
	if got.ID != message.ID || got.Body != message.Body || got.Dir != convo.In {
		t.Fatalf("stored %+v", got)
	}

	kind, body, err := wire.NewConn(readWriter{&out, io.Discard}).ReadFrame()
	if err != nil || kind != wire.KindAck {
		t.Fatalf("answer = kind %d, %v", kind, err)
	}
	r := wire.NewReader(body)
	count, err := r.Uint()
	if err != nil || count != 1 {
		t.Fatalf("ack count = %d, %v", count, err)
	}
	id, err := r.String(256)
	if err != nil || id != message.ID || !r.Done() {
		t.Fatalf("ack id = %q, %v", id, err)
	}
}

func TestLinkWithoutAStoreRejectsArrivals(t *testing.T) {
	var out bytes.Buffer
	l := New(Into{})
	at := arch.Session{Conn: wire.NewConn(readWriter{bytes.NewReader(nil), &out})}
	if err := l.Serve(t.Context(), at); err != nil {
		t.Fatalf("Serve(): %v", err)
	}
	kind, _, err := wire.NewConn(readWriter{&out, io.Discard}).ReadFrame()
	if err != nil || kind != wire.KindReject {
		t.Fatalf("answer = kind %d, %v", kind, err)
	}
}
