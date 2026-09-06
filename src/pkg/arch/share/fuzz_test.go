package share

import "testing"

func FuzzDecodeOffer(f *testing.F) {
	f.Add(offer{Items: []Item{{Name: "notes", Size: 4, Mode: 0o600}}}.encode())
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, body []byte) {
		got, err := decodeOffer(body)
		if err != nil {
			return
		}
		if len(got.Items) > maxItems {
			t.Fatalf("decoded %d offered items", len(got.Items))
		}
	})
}

func FuzzDecodeResume(f *testing.F) {
	f.Add(resume{At: []int64{0, 42}}.encode())
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, body []byte) {
		got, err := decodeResume(body)
		if err != nil {
			return
		}
		if len(got.At) > maxItems {
			t.Fatalf("decoded %d resume offsets", len(got.At))
		}
		for _, at := range got.At {
			if at < 0 {
				t.Fatalf("decoded negative resume offset %d", at)
			}
		}
	})
}
