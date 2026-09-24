package live

import "testing"

func FuzzDecodeResize(f *testing.F) {
	f.Add(Resize{Cols: 80, Rows: 24}.encode())
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, body []byte) {
		got, err := decodeResize(body)
		if err != nil {
			return
		}
		cols, rows, ok := got.shape()
		if ok && (cols < leastSide || rows < leastSide || cols > mostSide || rows > mostSide) {
			t.Fatalf("decoded live size %dx%d", cols, rows)
		}
	})
}
