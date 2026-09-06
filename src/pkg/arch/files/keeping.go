package files

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	gopath "path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/keep"
)

// The directory on disk, and the two ways it and the history get out of step.
//
// Somebody edits a file, and that is a change to sign. Changes arrive, and the folder they make has
// to be put on the disk. Telling the two apart is the whole difficulty, and what tells them apart is
// a record of what this machine last agreed each path was: a file that is not what that record says
// is a file somebody edited here, and a file the record has never heard of is a file that has not
// arrived rather than one somebody deleted.
//
// Refused, and not half done: hard links, sparse files, owner, group and extended attributes,
// differences inside a file, three-way merging of anything that is not lines, and any file that is
// being written while it is read. An empty directory is not carried either — a folder is the files
// in it, and a directory is made when a file needs one.

// Every is how often each folder is held up against its history. A watcher on the directory would
// notice a save sooner and is worth having later; this is soon enough for somebody saving a file.
const Every = 2 * time.Second

// Wanted is one file a folder is missing, and where its bytes should end up.
type Wanted struct {
	// Path is the namespace it belongs to, and Name is the path inside it.
	Path string
	Name string
	Size int64
	// Sum is the digest of the version wanted, which is what names the part file it fills, so a
	// fetch that is cut off carries on rather than starting again.
	Sum []byte
	// Into is where on this disk the bytes go.
	Into string
}

// mark is what this machine last agreed one path was.
type mark struct {
	Sum  [32]byte
	Size int64
	At   int64
	Exec bool
	File os.FileInfo
}

// keeper holds one folder: the directory, its history, and what has passed between them.
type keeper struct {
	path string
	dir  string
	log  *history.Log
	// held is what this machine last agreed each path was. It outlives the process, because a file
	// edited while drop was not running has to be told from one drop itself wrote before it
	// stopped.
	held  map[string]mark
	known bool
	// wrote is what was last written down, so an unchanged record is not rewritten every round.
	wrote      [32]byte
	wroteKnown bool
}

// once brings the folder and the history level with each other, and says whether a change of this
// machine's own was recorded.
func (k *keeper) once(fetch func(Wanted) error) (bool, error) {
	if err := k.recall(); err != nil {
		return false, err
	}

	now, err := scan(k.dir, k.held)
	if err != nil {
		return false, err
	}
	want, err := k.folder()
	if err != nil {
		return false, err
	}

	made := false
	if mine := k.mine(now, want); len(mine) > 0 {
		if err := k.record(mine); err != nil {
			return false, err
		}
		if want, err = k.folder(); err != nil {
			return true, err
		}
		made = true
	}

	trouble := k.apply(want, now, fetch)

	// Once everybody holding this folder has caught up, what it came to stands in place of every
	// change that made it. The history decides the moment; the folder only says what it holds.
	if trouble == nil && k.log.Folding() {
		if body, fits := Snapshot(want); fits {
			if _, err := k.log.Fold(body); err != nil {
				return made, fmt.Errorf("folding the history of %s: %w", k.dir, err)
			}
		}
	}
	if err := k.remember(); err != nil {
		return made, err
	}
	return made, trouble
}

// folder is what the history says the directory holds.
func (k *keeper) folder() (Folder, error) {
	changes, err := k.log.Ordered()
	if err != nil {
		return nil, fmt.Errorf("reading the history of %s: %w", k.dir, err)
	}
	return Changed(changes), nil
}

// mine is what somebody did in this directory since the last time it was looked at.
//
// A path that is not what the record says is a path somebody edited here. A path the record knew
// and the disk no longer has is one somebody deleted. A path the record never knew is neither: it
// is a file that has not arrived, and calling it a deletion would delete it everywhere.
func (k *keeper) mine(now map[string]mark, want Folder) []Edit {
	var out []Edit

	for _, path := range named(now) {
		m := now[path]
		if was, knew := k.held[path]; knew && was.Sum == m.Sum && was.Exec == m.Exec {
			continue
		}
		held := k.told(path, m)
		if there, said := want[path]; said && there.same(held) {
			continue
		}
		out = append(out, Edit{Path: path, Held: held})
	}

	when := time.Now().UnixNano()
	for _, path := range named(k.held) {
		if _, still := now[path]; still {
			continue
		}
		if there, said := want[path]; said && there.Gone {
			continue
		}
		out = append(out, Edit{Path: path, Held: Held{Gone: true, At: when}})
	}
	return out
}

// told is what to say about one path: what it weighs and what it hashes to, and its bytes as well
// when they are small enough and plain enough to travel in a change.
func (k *keeper) told(path string, m mark) Held {
	h := Held{Size: m.Size, Sum: m.Sum, Exec: m.Exec, At: m.At}
	if m.Size > MaxInline {
		return h
	}

	raw, err := readInline(filepath.Join(k.dir, filepath.FromSlash(path)))
	if err != nil || blake3.Sum256(raw) != m.Sum || !carries(raw) {
		return h
	}
	h.Body = raw
	return h
}

// record signs what happened here and hands it to the history, in as many changes as it takes to
// stay inside what one change may carry.
func (k *keeper) record(list []Edit) error {
	for _, edit := range list {
		if len(edit.Path) > MaxRel {
			return fmt.Errorf("recording %s: %s is %d bytes, and a path may be %d",
				k.dir, edit.Path, len(edit.Path), MaxRel)
		}
	}

	for len(list) > 0 {
		n := len(list)
		for n > 1 && len(encodeEdits(list[:n])) > history.MaxBody {
			n /= 2
		}
		body := encodeEdits(list[:n])
		if len(body) > history.MaxBody {
			return fmt.Errorf("recording %s: %s alone is %d bytes, and a change may be %d",
				k.dir, list[0].Path, len(body), history.MaxBody)
		}

		c, err := history.Sign(k.log.At(), body, k.log.Heads())
		if err != nil {
			return fmt.Errorf("recording %s: %w", k.dir, err)
		}
		if _, err := k.log.Add(c); err != nil {
			return fmt.Errorf("recording %s: %w", k.dir, err)
		}
		list = list[n:]
	}
	return nil
}

// apply makes the directory what the history says it is.
//
// Whatever can be answered out of bytes already on this disk is answered first, and deletions come
// last, so that a file which moved is still lying under its old name when the new name looks for
// its bytes. One path that cannot be made right does not stop the rest: what is wrong with it is
// answered at the end, and the round after this one tries again.
func (k *keeper) apply(want Folder, now map[string]mark, fetch func(Wanted) error) error {
	root, err := os.OpenRoot(k.dir)
	if err != nil {
		return fmt.Errorf("opening %s: %w", k.dir, err)
	}
	defer func() { _ = root.Close() }()

	done, trouble := k.recover(root, want, now)

	for _, path := range Paths(want) {
		h := want[path]
		if h.Gone || done[path] {
			continue
		}
		if settled(now, path, h) {
			k.held[path] = now[path]
			continue
		}
		if err := k.put(root, path, h, fetch); err != nil {
			trouble = append(trouble, err)
		}
	}

	for _, path := range Paths(want) {
		if !want[path].Gone {
			continue
		}
		if _, on := now[path]; !on {
			delete(k.held, path)
			continue
		}
		// Only a file this machine knew it had is a file somebody deleted. One it never had is one
		// that has not arrived, and removing that would be answering a question nobody asked.
		if _, knew := k.held[path]; !knew {
			continue
		}
		if err := root.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			trouble = append(trouble, fmt.Errorf("removing %s: %w", path, err))
			continue
		}
		if err := syncDirectory(root, gopath.Dir(path)); err != nil {
			trouble = append(trouble, fmt.Errorf("syncing removal of %s: %w", path, err))
			continue
		}
		delete(k.held, path)
	}
	return errors.Join(trouble...)
}

// recover puts every path whose bytes are already somewhere on this disk where it belongs, without
// a byte on the wire.
//
// That one lookup by digest is what recovers a rename, a copy and a move alike: a rename is a
// delete and a create, so when the create is answered the bytes are still here under the old name.
//
// Every copy is made out of the files as they stand and only put into place once all of them are
// made. Two files that swapped names, or a version kept beside the one that took its place, would
// otherwise be read after they had already been written over.
func (k *keeper) recover(root *os.Root, want Folder, now map[string]mark) (map[string]bool, []error) {
	var trouble []error
	by, made, done := digests(now), map[string]string{}, map[string]bool{}

	for _, path := range Paths(want) {
		h := want[path]
		if h.Gone || h.Body != nil || settled(now, path, h) {
			continue
		}
		from := by[h.Sum]
		if from == "" || from == path {
			continue
		}

		part := parting(path)
		if err := branches(root, path); err != nil {
			trouble = append(trouble, err)
			continue
		}
		if err := copyOut(root, from, part); err != nil {
			trouble = append(trouble, err)
			continue
		}
		made[path] = part
	}

	for _, path := range named(made) {
		if err := root.Rename(made[path], path); err != nil {
			_ = root.Remove(made[path])
			trouble = append(trouble, fmt.Errorf("renaming %s: %w", made[path], err))
			continue
		}
		if err := syncLanding(root, path); err != nil {
			trouble = append(trouble, fmt.Errorf("syncing %s: %w", path, err))
			continue
		}
		done[path] = true
		if err := k.dressed(root, path, want[path], want[path].Sum); err != nil {
			trouble = append(trouble, err)
		}
	}
	return done, trouble
}

// put makes one path what the changes say it is: the bytes out of the change when it carries them,
// and otherwise the bytes from whoever has them.
func (k *keeper) put(root *os.Root, path string, h Held, fetch func(Wanted) error) error {
	if err := branches(root, path); err != nil {
		return err
	}

	sum := h.Sum
	switch {
	case h.Body != nil:
		if err := written(root, path, h.Body); err != nil {
			return err
		}
	case fetch == nil:
		return nil
	default:
		at := filepath.Join(k.dir, filepath.FromSlash(path))
		w := Wanted{Path: k.path, Name: path, Size: h.Size, Sum: h.Sum[:], Into: at}
		if err := fetch(w); err != nil {
			return fmt.Errorf("fetching %s: %w", path, err)
		}
		// What the change asks for is what has to arrive. The digest is inside something somebody
		// signed, so it is the one account of this version that cannot have been made up by
		// whoever happens to be sending the bytes — and a holder is not always the author.
		//
		// Bytes that do not match are not a version of this file: they are whatever the sender
		// felt like, on a path the folder is missing, with the mode the change asks for put on
		// them afterwards. So they go, and the round tries again, which reaches a different holder.
		landed, _, err := sumOf(at)
		if err != nil {
			return err
		}
		if landed != h.Sum {
			_ = root.Remove(path)
			return fmt.Errorf("fetching %s: it arrived as %x and the change asks for %x",
				path, landed[:6], h.Sum[:6])
		}
		sum = landed
	}
	return k.dressed(root, path, h, sum)
}

// dressed puts the mode and the time on a path that has just been written, and writes down what is
// now there.
func (k *keeper) dressed(root *os.Root, path string, h Held, sum [32]byte) error {
	mode := os.FileMode(0o600)
	if h.Exec {
		mode = 0o700
	}
	if err := root.Chmod(path, mode); err != nil {
		return fmt.Errorf("setting the mode of %s: %w", path, err)
	}
	if h.At > 0 {
		when := time.Unix(0, h.At)
		_ = root.Chtimes(path, when, when)
	}

	stat, err := root.Stat(path)
	if err != nil {
		return fmt.Errorf("looking at %s: %w", path, err)
	}
	k.held[path] = mark{Sum: sum, Size: stat.Size(), At: stat.ModTime().UnixNano(), Exec: h.Exec, File: stat}
	return nil
}

// settled reports whether a path already holds what the changes say it holds.
func settled(now map[string]mark, path string, h Held) bool {
	m, on := now[path]
	return on && m.Sum == h.Sum && m.Exec == h.Exec
}

// scan is what the directory holds now, by path, slash-separated and relative to it.
//
// Plain files only: a folder several machines hold is the files in it, and a link, a socket or a
// device is a thing on this machine that means nothing on another. Part files are passed over
// because they are transfers in flight rather than files. A file whose length and time are what the
// record says they were is not read again, and a file that moves while it is being read is left for
// the next round.
func scan(dir string, was map[string]mark) (map[string]mark, error) {
	out := map[string]mark{}

	err := filepath.WalkDir(dir, func(at string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if at == dir {
			return nil
		}

		rel, err := filepath.Rel(dir, at)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		retain := func() {
			if held, knew := was[rel]; knew {
				out[rel] = held
			}
		}
		if d.IsDir() {
			if _, knew := was[rel]; knew {
				retain()
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			retain()
			return nil
		}
		if partial(gopath.Base(rel)) {
			return nil
		}
		if len(rel) > MaxRel {
			return nil
		}
		if len(out) >= MaxPaths {
			return fmt.Errorf("%s holds more than the %d files one folder carries", dir, MaxPaths)
		}

		stat, err := d.Info()
		if err != nil {
			retain()
			return nil
		}
		if !singleLinked(stat) {
			retain()
			return nil
		}
		m := mark{Size: stat.Size(), At: stat.ModTime().UnixNano(), Exec: stat.Mode().Perm()&0o111 != 0, File: stat}
		if held, knew := was[rel]; knew && held.File != nil && os.SameFile(held.File, stat) && held.Size == m.Size && held.At == m.At {
			m.Sum = held.Sum
			out[rel] = m
			return nil
		}

		sum, readAt, err := sumOf(at)
		if err != nil {
			retain()
			return nil
		}
		if !os.SameFile(stat, readAt) || readAt.Size() != m.Size || readAt.ModTime().UnixNano() != m.At {
			retain()
			return nil
		}
		m.Sum = sum
		out[rel] = m
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	return out, nil
}

// digests is one path per digest, so that bytes already on this disk can be found by what they are.
func digests(now map[string]mark) map[[32]byte]string {
	out := make(map[[32]byte]string, len(now))
	for _, path := range named(now) {
		if _, held := out[now[path].Sum]; !held {
			out[now[path].Sum] = path
		}
	}
	return out
}

// branches makes the directories a path needs, through the folder rather than through a name.
func branches(root *os.Root, path string) error {
	dir := gopath.Dir(path)
	if dir == "." {
		return nil
	}

	at := ""
	for _, part := range strings.Split(dir, "/") {
		at = gopath.Join(at, part)
		err := root.Mkdir(at, 0o700)
		if err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("making %s: %w", at, err)
			}
			continue
		}
		if err := syncDirectory(root, gopath.Dir(at)); err != nil {
			return fmt.Errorf("syncing %s: %w", at, err)
		}
	}
	return nil
}

// parting is where a file waits while it is being put together, beside where it lands.
func parting(path string) string {
	return gopath.Join(gopath.Dir(path), "."+gopath.Base(path)+".arriving.part")
}

// written puts bytes at a path, whole or not at all.
func written(root *os.Root, path string, body []byte) error {
	part := parting(path)

	out, err := freshPart(root, part)
	if err != nil {
		return fmt.Errorf("opening %s: %w", part, err)
	}
	if _, err := out.Write(body); err != nil {
		_ = out.Close()
		_ = root.Remove(part)
		return fmt.Errorf("writing %s: %w", part, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = root.Remove(part)
		return fmt.Errorf("syncing %s: %w", part, err)
	}
	if err := out.Close(); err != nil {
		_ = root.Remove(part)
		return fmt.Errorf("closing %s: %w", part, err)
	}
	if err := root.Rename(part, path); err != nil {
		_ = root.Remove(part)
		return fmt.Errorf("renaming %s: %w", part, err)
	}
	return syncLanding(root, path)
}

// copyOut puts the bytes already at one path into a part file, ready to be put into place.
func copyOut(root *os.Root, from, part string) error {
	held, err := root.Open(from)
	if err != nil {
		return fmt.Errorf("opening %s: %w", from, err)
	}
	defer func() { _ = held.Close() }()

	out, err := freshPart(root, part)
	if err != nil {
		return fmt.Errorf("opening %s: %w", part, err)
	}
	if _, err := io.Copy(out, held); err != nil {
		_ = out.Close()
		_ = root.Remove(part)
		return fmt.Errorf("writing %s: %w", part, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = root.Remove(part)
		return fmt.Errorf("syncing %s: %w", part, err)
	}
	if err := out.Close(); err != nil {
		_ = root.Remove(part)
		return fmt.Errorf("closing %s: %w", part, err)
	}
	return nil
}

func freshPart(root *os.Root, part string) (*os.File, error) {
	if err := root.Remove(part); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return root.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

// sumOf is what a stable regular file holds, as one number.
func sumOf(at string) ([32]byte, os.FileInfo, error) {
	file, before, err := openRegular(at)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("reading %s: %w", at, err)
	}
	defer func() { _ = file.Close() }()

	sum := blake3.New(32, nil)
	if _, err := io.Copy(sum, file); err != nil {
		return [32]byte{}, nil, fmt.Errorf("reading %s: %w", at, err)
	}
	if err := stillCurrent(at, file, before); err != nil {
		return [32]byte{}, nil, fmt.Errorf("reading %s: %w", at, err)
	}
	return [32]byte(sum.Sum(nil)), before, nil
}

func readInline(at string) ([]byte, error) {
	file, before, err := openRegular(at)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if before.Size() > MaxInline {
		return nil, fmt.Errorf("it is larger than %d bytes", MaxInline)
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxInline+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxInline {
		return nil, fmt.Errorf("it is larger than %d bytes", MaxInline)
	}
	if err := stillCurrent(at, file, before); err != nil {
		return nil, err
	}
	return raw, nil
}

func openRegular(at string) (*os.File, os.FileInfo, error) {
	file, err := os.OpenFile(at, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !stat.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, errors.New("it is not a regular file")
	}
	if !singleLinked(stat) {
		_ = file.Close()
		return nil, nil, errors.New("it has more than one hard link")
	}
	hasHole, err := sparse(file, stat.Size())
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if hasHole {
		_ = file.Close()
		return nil, nil, errors.New("it is sparse")
	}
	return file, stat, nil
}

func singleLinked(stat os.FileInfo) bool {
	raw, ok := stat.Sys().(*syscall.Stat_t)
	return !ok || raw.Nlink <= 1
}

func sparse(file *os.File, size int64) (bool, error) {
	if size <= 0 {
		return false, nil
	}
	hole, err := unix.Seek(int(file.Fd()), 0, unix.SEEK_HOLE)
	switch {
	case err == nil:
		if _, err := unix.Seek(int(file.Fd()), 0, unix.SEEK_SET); err != nil {
			return false, err
		}
		return hole < size, nil
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.ENXIO):
		return false, nil
	default:
		return false, err
	}
}

func stillCurrent(at string, file *os.File, before os.FileInfo) error {
	after, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(at)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(before, current) ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) ||
		current.Size() != before.Size() || !current.ModTime().Equal(before.ModTime()) {
		return errors.New("it changed while it was read")
	}
	return nil
}

// partial reports whether a name is a transfer in flight rather than a file.
func partial(name string) bool {
	return strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".part")
}

// named is a map's keys in the one order they are walked.
func named[T any](of map[string]T) []string {
	out := make([]string, 0, len(of))
	for key := range of {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// stood is one path as the record of what this machine agreed is written down.
type stood struct {
	Sum  string `json:"sum"`
	Size int64  `json:"size"`
	At   int64  `json:"at"`
	Exec bool   `json:"exec,omitempty"`
}

// recall reads back what this machine last agreed the folder was, once.
func (k *keeper) recall() error {
	if k.known {
		return nil
	}

	hash := blake3.New(32, nil)
	var held map[string]mark
	_, err := keep.ReadFileWith(k.mark(), maxHeldSize, func(from io.Reader) error {
		var err error
		held, err = readHeld(io.TeeReader(from, hash), MaxPaths)
		return err
	})
	if errors.Is(err, os.ErrNotExist) {
		k.held, k.known = map[string]mark{}, true
		return nil
	}
	if malformedHeld(err) {
		k.held, k.known = map[string]mark{}, true
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", k.mark(), err)
	}

	k.held, k.known = held, true
	copy(k.wrote[:], hash.Sum(nil))
	k.wroteKnown = true
	return nil
}

func readHeld(from io.Reader, maxPaths int) (map[string]mark, error) {
	decoder := json.NewDecoder(&heldJSONReader{from: from})
	start, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if start != json.Delim('{') {
		return nil, fmt.Errorf("%w: it is not an object", errHeldShape)
	}

	held := make(map[string]mark)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		path, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("%w: a path is not a string", errHeldShape)
		}
		var one stood
		if err := decoder.Decode(&one); err != nil {
			return nil, err
		}

		cleaned, err := clean(path)
		if err != nil || cleaned != path || path == "." || one.Size < 0 {
			delete(held, path)
			continue
		}
		sum, err := hex.DecodeString(one.Sum)
		if err != nil || len(sum) != len(mark{}.Sum) {
			delete(held, path)
			continue
		}
		if _, exists := held[path]; !exists && len(held) >= maxPaths {
			return nil, fmt.Errorf("a held record names more than the %d-path limit", maxPaths)
		}
		held[path] = mark{Sum: [32]byte(sum), Size: one.Size, At: one.At, Exec: one.Exec}
	}
	end, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if end != json.Delim('}') {
		return nil, fmt.Errorf("%w: it does not end as an object", errHeldShape)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: another value follows it", errHeldShape)
		}
		return nil, err
	}
	return held, nil
}

var errHeldShape = errors.New("invalid held record")
var errHeldToken = errors.New("a held record contains an oversized token")

type heldJSONReader struct {
	from      io.Reader
	inString  bool
	escaped   bool
	tokenSize int
	failed    error
}

func (r *heldJSONReader) Read(into []byte) (int, error) {
	if r.failed != nil {
		return 0, r.failed
	}
	n, readErr := r.from.Read(into)
	for i, b := range into[:n] {
		if r.inString {
			if b == '"' && !r.escaped {
				r.inString, r.tokenSize = false, 0
				continue
			}
			r.tokenSize++
			if r.tokenSize > maxHeldString {
				return r.fail(i)
			}
			if r.escaped {
				r.escaped = false
			} else if b == '\\' {
				r.escaped = true
			}
			continue
		}

		switch b {
		case '"':
			r.inString, r.tokenSize = true, 0
		case ' ', '\t', '\r', '\n', '{', '}', '[', ']', ',', ':':
			r.tokenSize = 0
		default:
			r.tokenSize++
			if r.tokenSize > maxHeldAtom {
				return r.fail(i)
			}
		}
	}
	return n, readErr
}

func (r *heldJSONReader) fail(read int) (int, error) {
	r.failed = errHeldToken
	if read == 0 {
		return 0, r.failed
	}
	return read, nil
}

func malformedHeld(err error) bool {
	if err == nil {
		return false
	}
	var syntax *json.SyntaxError
	var typed *json.UnmarshalTypeError
	return errors.Is(err, errHeldShape) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.As(err, &syntax) || errors.As(err, &typed)
}

// remember writes down what the folder now is by this machine's own doing.
func (k *keeper) remember() error {
	hash := blake3.New(32, nil)
	if err := writeHeld(io.Discard, hash, k.held); err != nil {
		return fmt.Errorf("writing %s: %w", k.mark(), err)
	}
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	if k.wroteKnown && sum == k.wrote {
		return nil
	}

	hash.Reset()
	if err := keep.ReplaceWith(k.mark(), func(to io.Writer) error {
		return writeHeld(to, hash, k.held)
	}); err != nil {
		return fmt.Errorf("writing %s: %w", k.mark(), err)
	}
	copy(k.wrote[:], hash.Sum(nil))
	k.wroteKnown = true
	return nil
}

func writeHeld(to io.Writer, hash io.Writer, held map[string]mark) error {
	out := io.MultiWriter(to, hash)
	if _, err := io.WriteString(out, "{"); err != nil {
		return err
	}
	for i, path := range named(held) {
		if i > 0 {
			if _, err := io.WriteString(out, ","); err != nil {
				return err
			}
		}
		key, err := json.Marshal(path)
		if err != nil {
			return err
		}
		m := held[path]
		value, err := json.Marshal(stood{
			Sum: hex.EncodeToString(m.Sum[:]), Size: m.Size, At: m.At, Exec: m.Exec,
		})
		if err != nil {
			return err
		}
		if _, err := out.Write(key); err != nil {
			return err
		}
		if _, err := io.WriteString(out, ":"); err != nil {
			return err
		}
		if _, err := out.Write(value); err != nil {
			return err
		}
	}
	_, err := io.WriteString(out, "}")
	return err
}

const (
	maxHeldString = MaxRel * 6
	maxHeldAtom   = 32
	maxHeldSize   = int64(MaxPaths)*(maxHeldString+256) + 2
)

// mark is where what this machine agreed is kept: beside the history, because it is about the thing
// rather than about the directory, and a folder moved elsewhere is the same folder.
func (k *keeper) mark() string { return filepath.Join(k.log.Dir(), "held") }
