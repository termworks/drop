package files

import "testing"

// says is a declaration of a handful of settings, and nothing else.
type says struct {
	dir      string
	writable bool
}

func (s says) String(key string) (string, bool) {
	if key != "dir" || s.dir == "" {
		return "", false
	}
	return s.dir, true
}

func (s says) Bool(key string) (bool, bool) {
	if key != "writable" {
		return false, false
	}
	return s.writable, true
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
