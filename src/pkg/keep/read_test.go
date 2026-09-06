package keep

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadFileReadsWithinItsLimit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "state")
	if err := os.WriteFile(target, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	raw, err := ReadFile(link, 5)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	if string(raw) != "state" {
		t.Fatalf("ReadFile() = %q", raw)
	}
}

func TestReadFileRefusesAnOversizedSparseFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, MaxState+1); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadFile(file, MaxState); err == nil {
		t.Fatal("ReadFile() accepted an oversized file")
	}
}

func TestReadFileDoesNotWaitOnAPipe(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state")
	if err := syscallMkfifo(file, 0o600); err != nil {
		t.Skipf("making a pipe: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(file, MaxState)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ReadFile() accepted a pipe")
		}
	case <-time.After(time.Second):
		t.Fatal("ReadFile() waited on a pipe")
	}
}
