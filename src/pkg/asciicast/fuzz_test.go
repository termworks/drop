package asciicast

import (
	"bytes"
	"testing"
)

func FuzzReader(f *testing.F) {
	f.Add([]byte("{\"version\":2,\"width\":80,\"height\":24}\n[0.1,\"o\",\"hello\"]\n"))
	f.Add([]byte("{\"version\":2}\n[0.2,\"r\",\"120x40\"]\n"))
	f.Add([]byte("not a cast"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		reader, _, err := NewReader(bytes.NewReader(raw))
		if err != nil {
			return
		}
		for {
			event, err := reader.Next()
			if err != nil {
				return
			}
			if event.Kind == Resize {
				Size(event.Data)
			}
		}
	})
}

func FuzzSize(f *testing.F) {
	f.Add("120x40")
	f.Add("0x0")
	f.Add("65535x65535")
	f.Add("wide x tall")

	f.Fuzz(func(t *testing.T, size string) {
		Size(size)
	})
}
