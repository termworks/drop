package note

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
//
// What is remembered is not only the bytes but which changes they were written from, because that
// is what a save has seen. Signing a save against the history as it stands now instead would name
// as read a change that landed a moment ago and is not in the file, and a version that says it came
// after a change it does not hold buries that change on every machine.

// busy is a file somebody is writing while it is being read. Merging half a save is worse than
// waiting a second, so it waits a second.
var busy = errors.New("it was being written while it was read")

// Still is how long a file has to have been left alone before what is in it is taken for a save.
//
// A writer that empties the file and then fills it, or that has written half of what it means to
// write, looks exactly like a file somebody finished saving: the size and the time are the same
// before and after the read. Only the time since it was last touched tells them apart.
const Still = Every / 2

// keeper holds one note: the file, its history, and what has passed between them.
type keeper struct {
	file string
	// at is the thing the history is about, so a namespace made again at the same path with the
	// same file is not written into the history of the one it replaced.
	at  string
	log *history.Log
	// wrote is what this machine last put in the file or last took out of it, by digest, and heads
	// is what the history said when it did. Both outlive the process, because a file edited while
	// drop was not running has to be told from one drop itself wrote before it stopped, and a save
	// made then still has to name what the person making it had in front of them.
	wrote [32]byte
	known bool
	heads []history.ID
	built bool
	// said is the mark as it was last written, so it is not rewritten every round.
	said string
}

// once brings the file and the history level with each other, and says whether this machine's own
// change was recorded.
func (k *keeper) once() (bool, error) {
	raw, there, err := steady(k.file)
	switch {
	// A file still being put down is not trouble and not a save. It is looked at again next round,
	// which is what the settling is for — and the file drop itself has just written is the ordinary
	// way to meet one.
	case errors.Is(err, busy):
		return false, nil
	case err != nil:
		return false, err
	}
	if err := k.recall(); err != nil {
		return false, err
	}

	made := false
	var trouble error
	if there && !(k.known && k.wrote == blake3.Sum256(raw)) {
		switch err := k.record(raw); {
		case err == nil:
			made = true
		default:
			if err := k.spare(raw); err != nil {
				return false, err
			}
			trouble = err
		}
	}

	heads := k.log.Heads()
	if k.built && there && same(heads, k.heads) {
		return made, trouble
	}
	if !there && len(heads) == 0 {
		return made, trouble
	}
	if err := k.behind(there, heads); err != nil {
		return made, err
	}

	changes, err := k.log.Ordered()
	if err != nil {
		return made, fmt.Errorf("reading the history of %s: %w", k.file, err)
	}

	body, aside, err := Whole(changes)
	if err != nil {
		return made, fmt.Errorf("keeping %s: %w", k.file, err)
	}
	if err := k.write(body, aside, raw, there, heads); err != nil {
		return made, err
	}

	// The merged file is the whole of what the history came to, so it is what a fold stands in
	// place of. The history decides whether this is the moment; a note only has to say what it says.
	if k.log.Folding() {
		if _, err := k.log.Fold(body); err != nil {
			return made, fmt.Errorf("folding the history of %s: %w", k.file, err)
		}
		k.heads = k.log.Heads()
	}
	return made, trouble
}

// behind refuses to write the file from a history that has lost what the file was made from.
//
// A record that will not decode is stepped over when the history is read back, and every change
// naming it goes with it. What is left is an older version of the note, and writing it over a file
// this machine itself wrote from the newer one would put the newer one nowhere at all.
func (k *keeper) behind(there bool, heads []history.ID) error {
	if !there {
		return nil
	}
	if len(heads) == 0 {
		return fmt.Errorf("keeping %s: the file is there and its history holds nothing", k.file)
	}
	if !k.built {
		return nil
	}
	for _, id := range k.heads {
		if !k.log.Has(id) {
			return fmt.Errorf("keeping %s: its history no longer holds the change the file was written from", k.file)
		}
	}
	return nil
}

// record signs what somebody saved and hands it to the history.
//
// What it names as seen is the history the file on disk was written from, and not the history as it
// stands this moment: a change that arrived since is one whoever saved this never saw, and a save
// that claims to have seen it is a save that replaces it.
func (k *keeper) record(raw []byte) error {
	if len(raw) > MaxSize {
		return fmt.Errorf("recording %s: it is %d bytes, and a note may be %d", k.file, len(raw), MaxSize)
	}

	c, err := history.Sign(k.log.At(), raw, k.seen())
	if err != nil {
		return fmt.Errorf("recording %s: %w", k.file, err)
	}
	if _, err := k.log.Add(c); err != nil {
		return fmt.Errorf("recording %s: %w", k.file, err)
	}
	return k.remember(raw, k.heads, k.built)
}

// seen is the history the file on disk was written from, and nothing at all until this machine has
// written it once — a file nobody here has ever written is a version of its own and came after
// nothing this machine can name.
func (k *keeper) seen() []history.ID {
	if !k.built {
		return nil
	}
	return k.heads
}

// spare keeps a save the history would not take, beside the file, before the history is written
// over it. It cannot be signed and it cannot stay, and losing it as well would be gratuitous.
func (k *keeper) spare(raw []byte) error {
	beside := k.file + ".unrecorded"
	if held, err := os.ReadFile(beside); err == nil && bytes.Equal(held, raw) {
		return nil
	}
	if err := keep.Replace(beside, raw); err != nil {
		return fmt.Errorf("keeping %s: %w", beside, err)
	}
	return nil
}

// write puts the file the history makes where the file is, and whatever would not merge beside it.
//
// What is written is remembered as this machine's own doing, along with the history it was written
// from, before anything reads the file again — which is what keeps two machines from handing one
// change back and forth for ever, and what lets the next save say what it was made against.
func (k *keeper) write(body []byte, aside []weave.Aside, raw []byte, there bool, heads []history.ID) error {
	if len(body) > MaxSize {
		return fmt.Errorf("writing %s: the history makes %d bytes of it, and a note may be %d", k.file, len(body), MaxSize)
	}
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
		return k.remember(body, heads, true)
	}
	if err := os.MkdirAll(filepath.Dir(k.file), 0o700); err != nil {
		return fmt.Errorf("writing %s: %w", k.file, err)
	}
	if err := keep.Replace(k.file, body); err != nil {
		return fmt.Errorf("writing %s: %w", k.file, err)
	}
	return k.remember(body, heads, true)
}

// remember writes down what the file now holds by this machine's own doing, and which changes it
// holds them from.
func (k *keeper) remember(body []byte, heads []history.ID, built bool) error {
	sum := blake3.Sum256(body)
	text := stamp(sum, heads, built)
	if k.known && k.wrote == sum && k.said == text {
		return nil
	}

	at := k.mark()
	if err := keep.Replace(at, []byte(text)); err != nil {
		return fmt.Errorf("writing %s: %w", at, err)
	}
	k.wrote, k.known, k.said = sum, true, text
	k.heads, k.built = heads, built
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

	lines := strings.Fields(string(raw))
	if len(lines) == 0 {
		return nil
	}
	sum, err := hex.DecodeString(lines[0])
	if err != nil || len(sum) != len(k.wrote) {
		return nil
	}

	heads := make([]history.ID, 0, len(lines)-1)
	for _, line := range lines[1:] {
		id, err := hex.DecodeString(line)
		if err != nil || len(id) != len(history.ID{}) {
			return nil
		}
		heads = append(heads, history.ID(id))
	}
	k.wrote, k.known = [32]byte(sum), true
	k.heads, k.built = heads, true
	k.said = stamp(k.wrote, heads, true)
	return nil
}

// stamp is a mark as it is written down: what the file holds, and the changes it was written from,
// one to a line.
func stamp(sum [32]byte, heads []history.ID, built bool) string {
	if !built {
		return hex.EncodeToString(sum[:])
	}

	var out strings.Builder
	out.WriteString(hex.EncodeToString(sum[:]))
	for _, id := range heads {
		out.WriteString("\n")
		out.WriteString(hex.EncodeToString(id[:]))
	}
	return out.String()
}

// mark is where what this machine last wrote is kept: beside the history, because it is about the
// thing rather than about the file, and a note moved to another directory is the same note.
func (k *keeper) mark() string { return filepath.Join(k.log.Dir(), "wrote") }

// steady reads a file, and refuses one that is still being written.
//
// A file that moved while it was read is one somebody is in the middle of saving. So is one that
// was touched a moment ago and has not settled: a writer that empties the file first, or that has
// written the first of several lines, shows the same size and the same time on both sides of the
// read, and half a save signed as a change is half a note on every other machine.
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
	if time.Since(before.ModTime()) < Still {
		return nil, false, fmt.Errorf("reading %s: %w", file, busy)
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
