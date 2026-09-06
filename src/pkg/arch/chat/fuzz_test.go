package chat

import "testing"

func FuzzDecodeStored(f *testing.F) {
	f.Add(encodeStored([]string{"one", "two"}))
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, body []byte) {
		ids, err := decodeStored(body)
		if err != nil {
			return
		}
		if len(ids) > MaxBatch {
			t.Fatalf("decoded %d receipt ids", len(ids))
		}
		for _, id := range ids {
			if len(id) > 256 {
				t.Fatalf("decoded a %d-byte receipt id", len(id))
			}
		}
	})
}
