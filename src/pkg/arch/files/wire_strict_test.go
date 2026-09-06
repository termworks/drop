package files

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/bresilla/drop/src/pkg/wire"
)

func TestFilesWireDecodersRefuseTrailingBytes(t *testing.T) {
	tests := []struct {
		name   string
		body   []byte
		decode func([]byte) error
	}{
		{"ready", ready{Writable: true}.encode(), func(body []byte) error { _, err := decodeReady(body); return err }},
		{"request", request{Op: opList, Size: wire.SizeUnknown}.encode(), func(body []byte) error { _, err := decodeRequest(body); return err }},
		{"reply", reply{OK: true}.encode(), func(body []byte) error { _, err := decodeReply(body); return err }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := append(append([]byte(nil), test.body...), 0)
			if err := test.decode(body); err == nil {
				t.Fatal("decoder accepted trailing bytes")
			}
		})
	}
}

func TestFilesWireDecodersRefuseModeOverflow(t *testing.T) {
	requestBody := wire.NewWriter()
	requestBody.Byte(opList)
	requestBody.String("")
	requestBody.String("")
	requestBody.Int(wire.SizeUnknown)
	requestBody.Uint(uint64(^uint32(0)) + 1)
	requestBody.Int(0)
	requestBody.Bytes(nil)
	requestBody.Int(0)
	if _, err := decodeRequest(requestBody.Body()); err == nil {
		t.Fatal("decodeRequest() accepted a mode wider than uint32")
	}

	replyBody := wire.NewWriter()
	replyBody.Bool(true)
	replyBody.String("")
	replyBody.Uint(1)
	replyBody.String("item")
	replyBody.Int(0)
	replyBody.Uint(uint64(^uint32(0)) + 1)
	replyBody.Bool(false)
	replyBody.Int(0)
	if _, err := decodeReply(replyBody.Body()); err == nil {
		t.Fatal("decodeReply() accepted a mode wider than uint32")
	}
}

func TestReplyDecoderRefusesNegativeEntrySizes(t *testing.T) {
	for _, entry := range []Entry{
		{Name: "item", Size: wire.SizeUnknown},
		{Name: "dir", Size: wire.SizeUnknown - 1, Dir: true},
	} {
		body := reply{OK: true, Entries: []Entry{entry}}.encode()
		if _, err := decodeReply(body); err == nil {
			t.Fatalf("decodeReply() accepted size %d for %+v", entry.Size, entry)
		}
	}
}

func TestGetRefusesAmbiguousFileMetadata(t *testing.T) {
	tests := []struct {
		name    string
		entries []Entry
	}{
		{"missing", nil},
		{"several", []Entry{{Name: "file", Size: 1}, {Name: "other", Size: 1}}},
		{"directory", []Entry{{Name: "file", Dir: true}}},
		{"another name", []Entry{{Name: "other", Size: 1}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			browser := browserAnsweredWith(t, reply{OK: true, Entries: test.entries})
			if err := browser.Get("file", filepath.Join(t.TempDir(), "file"), Want{}); err == nil {
				t.Fatal("Get() accepted ambiguous file metadata")
			}
		})
	}
}

func TestWritingRefusesUnexpectedReplyEntries(t *testing.T) {
	browser := browserAnsweredWith(t, reply{OK: true, Entries: []Entry{{Name: "other", Size: 1}}})
	if err := browser.Put("file", bytes.NewReader(nil), Given{}); err == nil {
		t.Fatal("Put() accepted unexpected reply entries")
	}
}

func browserAnsweredWith(t *testing.T, said reply) *Browsing {
	t.Helper()

	var answer bytes.Buffer
	conn := wire.NewConn(readWriter{&answer, &answer})
	if err := conn.WriteFrame(wire.KindReply, said.encode()); err != nil {
		t.Fatal(err)
	}
	return &Browsing{conn: wire.NewConn(readWriter{&answer, &bytes.Buffer{}}), writable: true}
}
