package note

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/weave"
)

// The file on disk, and the two ways it and the history get out of step.
//
// A person saves the file, and that is a change to sign. Changes arrive, and the file they make has
// to be written back. Telling the two apart is the whole difficulty: what this machine last put in
// the file is remembered, so that reading it back is not mistaken for somebody having edited it —
// which would be a change of its own, answered by a change of its own on the far side, for ever.

// busy is a file somebody is writing while it is being read. Merging half a save is worse than
// waiting a second, so it waits a second.
var busy = errors.New("it was being written while it was read")

// keeper holds one note: the file, its history, and what has passed between them.
type keeper struct {
	file string
	log  *history.Log
	// wrote is what this machine last put in the file or last took out of it, by digest. It
	// outlives the process, because a file edited while drop was not running has to be told from
	// one drop itself wrote before it stopped.
	wrote [32]byte
	known bool
	// heads is what the history said when the file was last written from it.
	heads []history.ID
	built bool
}

// once brings the file and the history level with each other, and says whether this machine's own
// change was recorded.
func (k *keeper) once(named func(string) string) (bool, error) {
	raw, there, err := steady(k.file)
	if err != nil {
		return false, err
	}
	if err := k.recall(); err != nil {
		return false, err
	}

	made := false
	if there && !(k.known && k.wrote == blake3.Sum256(raw)) {
		if err := k.record(raw); err != nil {
			return false, err
		}
		made = true
	}

	heads := k.log.Heads()
	if k.built && there && same(heads, k.heads) {
		return made, nil
	}
	if !there && len(heads) == 0 {
		return made, nil
	}

	changes, err := k.log.Ordered()
	if err != nil {
		return made, fmt.Errorf("reading the history of %s: %w", k.file, err)
	}

	body, aside := Whole(changes, named)
	if err := k.write(body, aside, raw, there); err != nil {
		return made, err
	}
	k.heads, k.built = heads, true
	return made, nil
}

// record signs what somebody saved and hands it to the history.
func (k *keeper) record(raw []byte) error {
	if len(raw) > MaxSize {
		return fmt.Errorf("recording %s: it is %d bytes, and a note may be %d", k.file, len(raw), MaxSize)
	}

	c, err := history.Sign(raw, k.log.Heads())
	if err != nil {
		return fmt.Errorf("recording %s: %w", k.file, err)
	}
	if _, err := k.log.Add(c); err != nil {
		return fmt.Errorf("recording %s: %w", k.file, err)
	}
	return k.remember(raw)
}

// write puts the file the history makes where the file is, and whatever would not merge beside it.
//
// What is written is remembered as this machine's own doing before anything reads the file again,
// which is what keeps two machines from handing one change back and forth for ever.
func (k *keeper) write(body []byte, aside []weave.Aside, raw []byte, there bool) error {
	for _, a := range aside {
		beside := k.file + "." + weave.Safe(a.Who)
		if held, err := os.ReadFile(beside); err == nil && bytes.Equal(held, a.Body) {
			continue
		}
		if err := keep.Replace(beside, a.Body); err != nil {
			return fmt.Errorf("keeping %s: %w", beside, err)
		}
	}

	if there && bytes.Equal(raw, body) {
		return k.remember(body)
	}
	if err := os.MkdirAll(filepath.Dir(k.file), 0o700); err != nil {
		return fmt.Errorf("writing %s: %w", k.file, err)
	}
	if err := keep.Replace(k.file, body); err != nil {
		return fmt.Errorf("writing %s: %w", k.file, err)
	}
	return k.remember(body)
}

// remember writes down what the file now holds by this machine's own doing.
func (k *keeper) remember(body []byte) error {
	sum := blake3.Sum256(body)
	if k.known && k.wrote == sum {
		return nil
	}

	at := k.mark()
	if err := keep.Replace(at, []byte(hex.EncodeToString(sum[:]))); err != nil {
		return fmt.Errorf("writing %s: %w", at, err)
	}
	k.wrote, k.known = sum, true
	return nil
}

// recall reads back what this machine last wrote, once.
func (k *keeper) recall() error {
	if k.known {
		return nil
	}

	at := k.mark()
	raw, err := os.ReadFile(at)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", at, err)
	}

	sum, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(sum) != len(k.wrote) {
		return nil
	}
	k.wrote, k.known = [32]byte(sum), true
	return nil
}

// mark is where what this machine last wrote is kept: beside the history, because it is about the
// thing rather than about the file, and a note moved to another directory is the same note.
func (k *keeper) mark() string { return filepath.Join(k.log.Dir(), "wrote") }

// steady reads a file, and refuses one that moved while it was being read.
func steady(file string) ([]byte, bool, error) {
	before, err := os.Stat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", file, err)
	}
	if before.IsDir() {
		return nil, false, fmt.Errorf("reading %s: it is a directory", file)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", file, err)
	}

	after, err := os.Stat(file)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", file, err)
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, false, fmt.Errorf("reading %s: %w", file, busy)
	}
	return raw, true, nil
}

// same reports whether a history is where it was.
func same(now, was []history.ID) bool {
	if len(now) != len(was) {
		return false
	}
	for i := range now {
		if now[i] != was[i] {
			return false
		}
	}
	return true
}
