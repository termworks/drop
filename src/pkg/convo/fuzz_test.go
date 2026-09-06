package convo

import (
	"testing"

	"github.com/bresilla/drop/src/pkg/wire"
)

func FuzzDecode(f *testing.F) {
	f.Add(Message{ID: "one", Kind: KindText, Body: "hello", At: 1}.Encode())
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, body []byte) {
		got, err := Decode(body)
		if err != nil {
			return
		}
		if len(got.ID) > 256 || len(got.Body) > MaxBody || len(got.Extra) > wire.MaxString {
			t.Fatalf("decoded message fields %d/%d/%d bytes", len(got.ID), len(got.Body), len(got.Extra))
		}
	})
}
