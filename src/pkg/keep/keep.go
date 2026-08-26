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
	"os"
	"path/filepath"
)

// Replace writes raw to file, leaving whatever was there untouched if anything goes wrong.
//
// The scratch file is made by CreateTemp, which is 0600, so what is written is never briefly
// readable by anybody else on the way in.
func Replace(file string, raw []byte) error {
	dir := filepath.Dir(file)

	scratch, err := os.CreateTemp(dir, filepath.Base(file)+".*")
	if err != nil {
		return fmt.Errorf("creating a scratch file in %s: %w", dir, err)
	}
	name := scratch.Name()
	defer os.Remove(name)

	if _, err := scratch.Write(raw); err != nil {
		scratch.Close()
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := scratch.Sync(); err != nil {
		scratch.Close()
		return fmt.Errorf("syncing %s: %w", name, err)
	}
	if err := scratch.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := os.Rename(name, file); err != nil {
		return fmt.Errorf("replacing %s: %w", file, err)
	}

	// A rename is atomic to a reader and not yet a fact on the disk. This is what makes it one.
	opened, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening %s: %w", dir, err)
	}
	defer opened.Close()

	if err := opened.Sync(); err != nil {
		return fmt.Errorf("syncing %s: %w", dir, err)
	}
	return nil
}
