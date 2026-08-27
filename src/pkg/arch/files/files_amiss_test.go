package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A folder several people write in can end up holding a file with a conflict in it. That has to
// reach a person, and from a process that keeps nothing — a listing has the config and the disk.
func TestAFolderWithAConflictInItSaysSo(t *testing.T) {
	dir := t.TempDir()
	f := New(Into{})

	if said := f.Amiss(Config{Dir: dir}); said != "" {
		t.Fatalf("an untouched folder says %q", said)
	}

	stuck := "one\n<<<<<<< alice\nALICE\n=======\nBOB\n>>>>>>> bob\nthree\n"
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte(stuck), 0o600); err != nil {
		t.Fatal(err)
	}
	said := f.Amiss(Config{Dir: dir})
	if !strings.Contains(said, "unsettled") || !strings.Contains(said, "notes.md") {
		t.Fatalf("a folder with a conflict in it says %q", said)
	}

	// A second one is counted, not listed.
	if err := os.MkdirAll(filepath.Join(dir, "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deep", "other.md"), []byte(stuck), 0o600); err != nil {
		t.Fatal(err)
	}
	if said := f.Amiss(Config{Dir: dir}); !strings.Contains(said, "1 more") {
		t.Fatalf("two conflicts say %q", said)
	}

	// Something that is not text is not searched for markers that happen to be in it.
	os.RemoveAll(filepath.Join(dir, "deep"))
	os.Remove(filepath.Join(dir, "notes.md"))
	if err := os.WriteFile(filepath.Join(dir, "x.bin"), append([]byte(stuck), 0x00, 0x01), 0o600); err != nil {
		t.Fatal(err)
	}
	if said := f.Amiss(Config{Dir: dir}); said != "" {
		t.Fatalf("a binary file that happens to hold markers says %q", said)
	}
}
