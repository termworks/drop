package wire

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	w := NewWriter()
	w.Byte(7)
	w.Bool(true)
	w.Uint(0)
	w.Uint(300)
	w.Int(-1)
	w.Int(1 << 40)
	w.String("laptop")
	w.Bytes([]byte{0xde, 0xad, 0xbe, 0xef})

	r := NewReader(w.Body())

	if v, err := r.Byte(); err != nil || v != 7 {
		t.Fatalf("Byte() = %d, %v", v, err)
	}
	if v, err := r.Bool(); err != nil || !v {
		t.Fatalf("Bool() = %v, %v", v, err)
	}
	if v, err := r.Uint(); err != nil || v != 0 {
		t.Fatalf("Uint() = %d, %v", v, err)
	}
	if v, err := r.Uint(); err != nil || v != 300 {
		t.Fatalf("Uint() = %d, %v", v, err)
	}
	// -1 is how an unknown size travels, so it has to survive exactly.
	if v, err := r.Int(); err != nil || v != -1 {
		t.Fatalf("Int() = %d, %v", v, err)
	}
	if v, err := r.Int(); err != nil || v != 1<<40 {
		t.Fatalf("Int() = %d, %v", v, err)
	}
	if v, err := r.String(MaxString); err != nil || v != "laptop" {
		t.Fatalf("String() = %q, %v", v, err)
	}
	if v, err := r.Bytes(16); err != nil || !bytes.Equal(v, []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("Bytes() = %x, %v", v, err)
	}
	if !r.Done() {
		t.Fatal("Done() is false with every field read back")
	}
}

// A small varint can claim a huge field. Reading one must fail rather than allocate.
func TestReaderRefusesAnOversizedField(t *testing.T) {
	w := NewWriter()
	w.Uint(1 << 30)

	r := NewReader(w.Body())
	if _, err := r.Bytes(1024); err == nil {
		t.Fatal("Bytes() accepted a field far past its limit")
	}
}

func TestReaderRefusesATruncatedField(t *testing.T) {
	// Claims eight bytes, carries two.
	body := append([]byte{8}, 0xaa, 0xbb)

	r := NewReader(body)
	if _, err := r.Bytes(1024); err == nil {
		t.Fatal("Bytes() accepted a field that runs past the end of the message")
	}
}

func TestReaderOnEmptyMessage(t *testing.T) {
	r := NewReader(nil)

	if _, err := r.Byte(); err == nil {
		t.Fatal("Byte() succeeded on an empty message")
	}
	if _, err := r.Uint(); err == nil {
		t.Fatal("Uint() succeeded on an empty message")
	}
}
