package wire

import "testing"

func TestControlBodiesRefuseTrailingBytes(t *testing.T) {
	checks := []struct {
		name string
		body []byte
		read func([]byte) error
	}{
		{"end", append(End{Size: 1}.Encode(), 0), func(body []byte) error { _, err := DecodeEnd(body); return err }},
		{"ack", append(Ack{OK: true}.Encode(), 0), func(body []byte) error { _, err := DecodeAck(body); return err }},
		{"reject", append(Reject{Reason: "no"}.Encode(), 0), func(body []byte) error { _, err := DecodeReject(body); return err }},
	}
	for _, check := range checks {
		if err := check.read(check.body); err == nil {
			t.Errorf("a %s with trailing bytes was accepted", check.name)
		}
	}
}

func TestAnEndRefusesANegativeActualSize(t *testing.T) {
	if _, err := DecodeEnd(End{Size: SizeUnknown}.Encode()); err == nil {
		t.Fatal("an end with a negative size was accepted")
	}
}
