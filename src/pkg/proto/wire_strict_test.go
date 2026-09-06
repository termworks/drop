package proto

import "testing"

func TestProtocolDecodersRefuseTrailingBytes(t *testing.T) {
	tests := []struct {
		name   string
		body   []byte
		decode func([]byte) error
	}{
		{"hello", Hello{}.encode(), func(body []byte) error { _, err := decodeHello(body); return err }},
		{"open", Opening{}.encode(), func(body []byte) error { _, err := decodeOpen(body); return err }},
		{"pair", pairMsg{}.encode(), func(body []byte) error { _, err := decodePairMsg(body); return err }},
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

func TestHelloDecoderRefusesNamespaceVersionOverflow(t *testing.T) {
	body := Hello{Serves: []Served{{Version: MaxVersion + 1}}}.encode()
	if _, err := decodeHello(body); err == nil {
		t.Fatal("decodeHello() accepted a namespace version over the limit")
	}
}
