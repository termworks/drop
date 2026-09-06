package book

import "testing"

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"alpha":{"id":"peer"}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := decode(raw, 8)
		if err != nil {
			return
		}
		if len(got) > 8 {
			t.Fatalf("decoded %d peers over an 8-peer limit", len(got))
		}
	})
}
