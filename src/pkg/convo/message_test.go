package convo

import "testing"

func TestMessageDecoderRefusesTrailingBytes(t *testing.T) {
	body := append(Message{ID: "id", Kind: KindText, Body: "hello"}.Encode(), 0)
	if _, err := Decode(body); err == nil {
		t.Fatal("Decode() accepted trailing bytes")
	}
}
