package files

import (
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
