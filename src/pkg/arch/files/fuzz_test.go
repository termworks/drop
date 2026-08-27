package files

import (
	"strings"
	"testing"
)

// A request from whoever opened the namespace, and a reply from whoever they opened it on. Both
// carry paths, and a path from the far end that reaches the disk is the whole danger here.
func FuzzDecodeRequest(f *testing.F) {
	f.Add(request{Op: 1, Name: "papers"}.encode())
	f.Add(request{Op: 2, Name: "../../etc/passwd"}.encode())
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, body []byte) {
		ask, err := decodeRequest(body)
		if err != nil {
			return
		}
		if len(ask.Name) > MaxRel || len(ask.To) > MaxRel {
			t.Fatalf("a %d byte request decoded a %d byte name", len(body), len(ask.Name))
		}
	})
}

func FuzzDecodeReply(f *testing.F) {
	f.Add(reply{OK: true, Entries: []Entry{{Name: "notes.md", Size: 4}}}.encode())
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, body []byte) {
		said, err := decodeReply(body)
		if err != nil {
			return
		}
		for _, e := range said.Entries {
			if len(e.Name) > MaxRel {
				t.Fatalf("an entry name came back at %d bytes, over the %d bound", len(e.Name), MaxRel)
			}
			// A name from the far end becomes a local path when the file is fetched. Whatever else
			// it is, it must not be a way out of the directory it was listed in.
			if strings.Contains(e.Name, "\x00") {
				t.Fatalf("an entry name carries a nul: %q", e.Name)
			}
		}
	})
}

// The edits several people make to one folder, as bytes off a change somebody signed.
func FuzzDecodeEdits(f *testing.F) {
	f.Add(encodeEdits([]Edit{{Path: "notes.md", Held: Held{Size: 4}}}))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, body []byte) {
		list, err := decodeEdits(body)
		if err != nil {
			return
		}
		for _, e := range list {
			switch {
			case e.Path == "":
				t.Fatal("an edit came back with no path")
			case strings.HasPrefix(e.Path, "/"):
				t.Fatalf("an edit came back with an absolute path: %q", e.Path)
			case strings.Contains(e.Path, "\x00"):
				t.Fatalf("an edit path carries a nul: %q", e.Path)
			}
			for _, part := range strings.Split(e.Path, "/") {
				if part == ".." {
					t.Fatalf("an edit path climbs out: %q", e.Path)
				}
			}
		}
	})
}
