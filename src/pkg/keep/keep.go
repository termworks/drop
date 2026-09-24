// Package keep writes a file so that a crash leaves the old one rather than half the new one.
//
// Everything drop remembers about who it has met lives in a small JSON file: who is paired and the
// secret shared with them, what has been granted, who has asked, who has knocked. Writing one in
// place truncates it first, so a machine that loses power mid-write comes back holding a file that
// will not parse — and for the address book that means every pairing on it, secrets included, is
// gone and cannot be made again without going back to both devices.
//
// So nothing is written in place. A scratch file beside the target takes the bytes, is flushed to
// the disk itself rather than to the kernel's opinion of it, and is then renamed over the target,
// which is one atomic step. The directory is flushed too, because a rename that the disk has not
// been told about is a rename that a crash undoes.
package keep

import (
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
)

// Replace writes raw to file, leaving whatever was there untouched if anything goes wrong.
//
// The scratch file is made by CreateTemp, which is 0600, so what is written is never briefly
// readable by anybody else on the way in.
func Replace(file string, raw []byte) error {
	return replace(file, func(scratch *os.File) error {
		if err := Room(scratch, int64(len(raw))); err != nil {
			return fmt.Errorf("reserving room for %s: %w", file, err)
		}
		if _, err := scratch.Write(raw); err != nil {
			return fmt.Errorf("writing %s: %w", scratch.Name(), err)
		}
		return nil
	})
}

// ReplaceWith atomically replaces file with bytes written without first holding them all in memory.
func ReplaceWith(file string, write func(io.Writer) error) error {
	return replace(file, func(scratch *os.File) error {
		if err := write(scratch); err != nil {
			return fmt.Errorf("writing %s: %w", scratch.Name(), err)
		}
		return nil
	})
}

func replace(file string, write func(*os.File) error) error {
	dir := filepath.Dir(file)

	scratch, err := os.CreateTemp(dir, filepath.Base(file)+".*")
	if err != nil {
		return fmt.Errorf("creating a scratch file in %s: %w", dir, err)
	}
	name := scratch.Name()
	defer func() { _ = os.Remove(name) }()

	if err := write(scratch); err != nil {
		_ = scratch.Close()
		return err
	}
	if err := scratch.Sync(); err != nil {
		_ = scratch.Close()
		return fmt.Errorf("syncing %s: %w", name, err)
	}
	if err := scratch.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := os.Rename(name, file); err != nil {
		return fmt.Errorf("replacing %s: %w", file, err)
	}

	return SyncDir(dir)
}

// Rename moves one kept file and flushes every directory whose entries changed.
func Rename(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("moving %s to %s: %w", from, to, err)
	}

	toDir := filepath.Dir(to)
	if err := SyncDir(toDir); err != nil {
		return err
	}
	fromDir := filepath.Dir(from)
	if fromDir != toDir {
		return SyncDir(fromDir)
	}
	return nil
}

// Remove unlinks one kept file and flushes its directory. An absent file is already removed.
func Remove(file string) error {
	if err := os.Remove(file); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("removing %s: %w", file, err)
	}
	return SyncDir(filepath.Dir(file))
}

// SyncDir flushes a directory and its entries to disk.
func SyncDir(dir string) error {
	opened, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening %s: %w", dir, err)
	}
	defer func() { _ = opened.Close() }()

	if err := opened.Sync(); err != nil {
		return fmt.Errorf("syncing %s: %w", dir, err)
	}
	return nil
}

// While runs something with a file to itself, so two processes changing it do not lose each
// other's work.
//
// A file read, changed and written back is three steps, and another process that writes between
// the first and the third has its change thrown away by the third. Every drop on a machine shares
// one address book, and `drop peer pair` is a separate process from the daemon — so a pairing made
// while the daemon happens to be writing is a pairing that never happened, with nothing to say so.
//
// The lock is advisory and taken on a file beside the one being changed, not on the file itself: a
// write here replaces the file by renaming another one onto it, and a lock held on the thing that
// gets replaced is a lock on a file nobody can see any more.
func While(file string, change func() error) error {
	at := file + ".lock"
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(at), err)
	}

	held, err := os.OpenFile(at, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", at, err)
	}
	defer func() { _ = held.Close() }()

	if err := unix.Flock(int(held.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("waiting for %s: %w", at, err)
	}
	defer func() { _ = unix.Flock(int(held.Fd()), unix.LOCK_UN) }()

	return change()
}
