package keep

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplaceAtomicallyChangesAFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "state.json")
	if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Replace(file, []byte("new")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(raw, []byte("new")) {
		t.Fatalf("stored %q, %v", raw, err)
	}
	stat, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("replacement mode = %v", stat.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("replacement left files %+v", entries)
	}
}

func TestReplaceDoesNotFollowTheTargetSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	target := filepath.Join(dir, "state")
	if err := os.WriteFile(victim, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, target); err != nil {
		t.Fatal(err)
	}
	if err := Replace(target, []byte("state")); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(victim); err != nil || string(raw) != "untouched" {
		t.Fatalf("symlink target = %q, %v", raw, err)
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "state" {
		t.Fatalf("replacement = %q, %v", raw, err)
	}
	stat, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !stat.Mode().IsRegular() {
		t.Fatalf("replacement mode = %v", stat.Mode())
	}
}

func TestReplaceCleansScratchAfterFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "occupied")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Replace(target, []byte("state")); err == nil {
		t.Fatal("Replace() replaced a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "occupied" {
		t.Fatalf("failed replacement left files %+v", entries)
	}
}

func TestRenameMovesAFile(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from")
	to := filepath.Join(dir, "to")
	if err := os.WriteFile(from, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Rename(from, to); err != nil {
		t.Fatalf("Rename(): %v", err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("the old name remains: %v", err)
	}
	if raw, err := os.ReadFile(to); err != nil || string(raw) != "kept" {
		t.Fatalf("the new name holds %q, %v", raw, err)
	}
}

func TestRenameMovesBetweenDirectories(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left")
	right := filepath.Join(dir, "right")
	if err := os.Mkdir(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(right, 0o700); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(left, "from")
	to := filepath.Join(right, "to")
	if err := os.WriteFile(from, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Rename(from, to); err != nil {
		t.Fatalf("Rename(): %v", err)
	}
	if raw, err := os.ReadFile(to); err != nil || string(raw) != "kept" {
		t.Fatalf("the new name holds %q, %v", raw, err)
	}
}

func TestRemoveForgetsAFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "gone")
	if err := os.WriteFile(file, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Remove(file); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the file remains: %v", err)
	}
	if err := Remove(file); err != nil {
		t.Fatalf("Remove() of an absent file: %v", err)
	}
}

func TestWhileSerializesChanges(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state")
	entered := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- While(file, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	second := make(chan error, 1)
	go func() { second <- While(file, func() error { return nil }) }()
	select {
	case err := <-second:
		t.Fatalf("second change crossed the lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second change did not enter after release")
	}

	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".lock") {
		t.Fatalf("lock files = %+v", entries)
	}
}
