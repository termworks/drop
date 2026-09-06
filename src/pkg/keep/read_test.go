package keep

import (
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestReadFileInfoIdentifiesTheOpenedFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(file, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}

	raw, info, err := ReadFileInfo(file, 5)
	if err != nil {
		t.Fatalf("ReadFileInfo(): %v", err)
	}
	current, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "state" || !os.SameFile(info, current) {
		t.Fatalf("ReadFileInfo() = %q, %v", raw, info)
	}
}

func TestReadFileWithStreamsAndChecksTheWholeFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(file, []byte("streamed state"), 0o600); err != nil {
		t.Fatal(err)
	}

	var head strings.Builder
	info, err := ReadFileWith(file, 14, func(from io.Reader) error {
		_, err := io.CopyN(&head, from, 8)
		return err
	})
	if err != nil {
		t.Fatalf("ReadFileWith(): %v", err)
	}
	if head.String() != "streamed" || info.Size() != 14 {
		t.Fatalf("ReadFileWith() read %q and reported %d bytes", head.String(), info.Size())
	}
}

func TestReadFileWithRefusesGrowthAfterTheCallbackReads(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(file, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFileWith(file, 5, func(from io.Reader) error {
		if _, err := io.Copy(io.Discard, from); err != nil {
			return err
		}
		return os.WriteFile(file, []byte("states"), 0o600)
	})
	if err == nil || !strings.Contains(err.Error(), "5-byte limit") {
		t.Fatalf("ReadFileWith() = %v, want growth past its limit refused", err)
	}
}

func TestReadFileWithRefusesReplacementDuringTheRead(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "state")
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFileWith(file, 3, func(from io.Reader) error {
		if _, err := io.Copy(io.Discard, from); err != nil {
			return err
		}
		return os.Rename(replacement, file)
	})
	if err == nil || !strings.Contains(err.Error(), "replaced while it was read") {
		t.Fatalf("ReadFileWith() = %v, want concurrent replacement refused", err)
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
