package files

import "testing"

// says is a declaration of a handful of settings, and nothing else.
type says struct {
	dir       string
	writable  bool
	text      map[string]string
	mentioned map[string]bool
}

func (s says) String(key string) (string, bool) {
	if key == "dir" && s.dir != "" {
		return s.dir, true
	}
	value, ok := s.text[key]
	return value, ok
}

func (s says) Bool(key string) (bool, bool) {
	if key == "writable" {
		return s.writable, true
	}
	return false, s.mentioned[key]
}

func TestFilesReadsTransferLimits(t *testing.T) {
	read, err := (&Files{}).Read(says{dir: "/x", text: map[string]string{
		"max_item": "2 MiB", "max_session": "3 GiB",
	}})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if read != (Config{Dir: "/x", MaxItemBytes: 2 << 20, MaxSessionBytes: 3 << 30}) {
		t.Fatalf("Read() = %+v", read)
	}
}

func TestFilesRefusesInvalidTransferLimits(t *testing.T) {
	for _, declaration := range []says{
		{dir: "/x", text: map[string]string{"max_item": "none"}},
		{dir: "/x", text: map[string]string{"max_item": "2 GiB", "max_session": "1 GiB"}},
		{dir: "/x", mentioned: map[string]bool{"max_item": true}},
	} {
		if _, err := (&Files{}).Read(declaration); err == nil {
			t.Fatalf("Read(%+v) accepted invalid limits", declaration)
		}
	}
}

func (says) Strings(string) ([]string, bool) { return nil, false }

// A files namespace with no directory has nothing to serve, and is refused where it is written.
func TestFilesNeedsADir(t *testing.T) {
	if _, err := (&Files{}).Read(says{}); err == nil {
		t.Fatal("Read() accepted a files namespace with no dir")
	}
}

// Read-only until the declaration says otherwise, and what may be said about it follows.
func TestFilesIsReadOnlyUnlessItSaysSo(t *testing.T) {
	read, err := (&Files{}).Read(says{dir: "/x"})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if read != (Config{Dir: "/x"}) {
		t.Fatalf("Read() = %+v", read)
	}
	if (&Files{}).Note(read).Writable {
		t.Error("a read-only mount said the far end may write")
	}

	write, err := (&Files{}).Read(says{dir: "/y", writable: true})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if !(&Files{}).Note(write).Writable {
		t.Error("a writable mount said the far end may not write")
	}
}
