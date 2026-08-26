package wire

import (
	"bytes"
	"io"
	"testing"
)

// Every frame body on the wire is bytes somebody else chose.
//
// A decoder is handed a length and a count before it has seen what backs them, so the interesting
// input is not a valid frame but a plausible one: a claim of a million entries in nine bytes, a
// string that says it is longer than what is left, a digest with no digest after it. What is
// asserted here is only that a decoder answers — the value it gives back is checked by the tests
// beside this one. A panic, a hang or an allocation that scales with a claim is the bug.

func FuzzDecodeEnd(f *testing.F) {
	f.Add(End{Size: 4, Digest: bytes.Repeat([]byte{1}, 32)}.Encode())
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, body []byte) {
		end, err := DecodeEnd(body)
		if err == nil && len(end.Digest) > len(body) {
			t.Fatalf("a %d byte body decoded a %d byte digest", len(body), len(end.Digest))
		}
	})
}

func FuzzDecodeAck(f *testing.F) {
	f.Add(Ack{OK: true, Reason: "fine"}.Encode())
	f.Add([]byte{1, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, body []byte) {
		ack, err := DecodeAck(body)
		if err == nil && len(ack.Reason) > len(body) {
			t.Fatalf("a %d byte body decoded a %d byte reason", len(body), len(ack.Reason))
		}
	})
}

func FuzzDecodeReject(f *testing.F) {
	f.Add(Reject{Reason: "no"}.Encode())
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = DecodeReject(body)
	})
}

// The reader underneath every decoder: each accessor is given a bound, and no bound may be
// exceeded by what comes back.
func FuzzReader(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, body []byte) {
		r := NewReader(body)
		for i := 0; i < 64 && !r.Done(); i++ {
			switch i % 6 {
			case 0:
				_, _ = r.Byte()
			case 1:
				_, _ = r.Bool()
			case 2:
				_, _ = r.Uint()
			case 3:
				_, _ = r.Int()
			case 4:
				got, err := r.Bytes(1024)
				if err == nil && len(got) > 1024 {
					t.Fatalf("Bytes(1024) gave back %d", len(got))
				}
			case 5:
				got, err := r.String(1024)
				if err == nil && len(got) > 1024 {
					t.Fatalf("String(1024) gave back %d", len(got))
				}
			}
		}
	})
}

// Framing itself: a header names a kind and a length, and a body follows or does not.
func FuzzConn(f *testing.F) {
	f.Add([]byte{KindOpen, 3, 'a', 'b', 'c', KindData, 1, 'x'})
	f.Add([]byte{KindData, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, stream []byte) {
		c := NewConn(readOnly{bytes.NewReader(stream)})
		for i := 0; i < 32; i++ {
			kind, size, err := c.ReadHeader()
			if err != nil {
				return
			}
			if size > MaxFrame {
				t.Fatalf("frame kind %d claimed %d bytes, over the limit", kind, size)
			}
			if err := c.Discard(size); err != nil {
				return
			}
		}
	})
}

// readOnly is a stream that reads and never writes, which is all framing needs to be driven.
type readOnly struct{ r io.Reader }

func (o readOnly) Read(p []byte) (int, error)  { return o.r.Read(p) }
func (o readOnly) Write(p []byte) (int, error) { return len(p), nil }
