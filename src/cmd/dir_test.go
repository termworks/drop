package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryReadsStopAtTheirEntryLimit(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"c", "a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := readDirUpTo(dir, 2); err == nil {
		t.Fatal("a directory over the entry limit was read")
	}
	entries, err := readDirUpTo(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Name() != "a" || entries[2].Name() != "c" {
		t.Fatalf("bounded directory entries = %v", entries)
	}
}
