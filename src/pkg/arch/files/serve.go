package files

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/wire"
)

// answer carries out one request. A refusal is a reply, not an error: the session stays open so the
// caller can ask for something else.
func (f *Files) answer(conn *wire.Conn, at arch.Session, dir *os.Root, writable bool, q request) error {
	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReply, reply{Reason: reason}.encode())
	}

	// The mount flag is the only thing that permits a write. Nothing here is per-operation.
	switch q.Op {
	case opPut, opReplace, opRemove, opMkdir, opMove:
		if !writable {
			return refuse(fmt.Sprintf("%s is read-only", at.Path))
		}
	}

	name, err := clean(q.Name)
	if err != nil {
		return refuse(err.Error())
	}

	// Only a listing may name the namespace itself. Nothing else is allowed to reach for the
	// directory the mount stands on.
	if name == "." && q.Op != opList {
		return refuse("that is the namespace itself")
	}

	switch q.Op {
	case opList:
		entries, err := listed(dir, name)
		if err != nil {
			return refuse(err.Error())
		}
		said := reply{OK: true, Entries: entries}.encode()
		if len(said) > wire.MaxFrame {
			return refuse(fmt.Sprintf("that listing is %d bytes, over the %d one reply carries", len(said), wire.MaxFrame))
		}
		return conn.WriteFrame(wire.KindReply, said)

	case opGet:
		return f.handGet(conn, dir, name, q)

	case opPut:
		return f.handPut(conn, at, dir, name, q)

	case opReplace:
		return f.handReplace(conn, at, dir, name, q)

	case opRemove:
		if err := dir.Remove(name); err != nil {
			return refuse(fmt.Sprintf("cannot remove %s: %v", name, unpath(err)))
		}
		return conn.WriteFrame(wire.KindReply, reply{OK: true}.encode())

	case opMkdir:
		if err := dir.Mkdir(name, 0o700); err != nil {
			return refuse(fmt.Sprintf("cannot make %s: %v", name, unpath(err)))
		}
		return conn.WriteFrame(wire.KindReply, reply{OK: true}.encode())

	case opMove:
		to, err := clean(q.To)
		if err != nil {
			return refuse(err.Error())
		}
		if to == "." {
			return refuse("that is the namespace itself")
		}
		if err := dir.Rename(name, to); err != nil {
			return refuse(fmt.Sprintf("cannot move %s: %v", name, unpath(err)))
		}
		return conn.WriteFrame(wire.KindReply, reply{OK: true}.encode())

	default:
		return refuse(fmt.Sprintf("operation %d is not one this serves", q.Op))
	}
}

// handGet answers a get and then writes the file out, from wherever the caller has already got to.
func (f *Files) handGet(conn *wire.Conn, dir *os.Root, name string, q request) error {
	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReply, reply{Reason: reason}.encode())
	}

	// A get is a regular file. A directory, a device or a pipe is not something to read down a
	// stream, and it is the opened file that is weighed, so no name can move under the answer.
	file, open, err := reading(dir, name)
	if err != nil {
		return refuse(fmt.Sprintf("cannot read %s: %v", name, unpath(err)))
	}
	defer file.Close()

	if open.IsDir() {
		return refuse(fmt.Sprintf("%s is a directory", name))
	}
	if !open.Mode().IsRegular() {
		return refuse(fmt.Sprintf("%s is not a file", name))
	}
	if q.From < 0 || q.From > open.Size() {
		return refuse(fmt.Sprintf("%s is %d bytes, and you asked to carry on from %d", name, open.Size(), q.From))
	}

	said := reply{OK: true, Entries: []Entry{entryOf(open)}}
	if err := conn.WriteFrame(wire.KindReply, said.encode()); err != nil {
		return err
	}
	return sendBody(conn, file, path.Base(name), open.Size(), q.From, f.into.Progress)
}

// handPut answers a put and then takes the file in.
func (f *Files) handPut(conn *wire.Conn, at arch.Session, dir *os.Root, name string, q request) error {
	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReply, reply{Reason: reason}.encode())
	}

	// What cannot be taken is refused before the caller starts sending, so a put that has nowhere to
	// land ends the round rather than the session.
	if reason := roomFor(dir, name); reason != "" {
		return refuse(reason)
	}
	if err := conn.WriteFrame(wire.KindReply, reply{OK: true}.encode()); err != nil {
		return err
	}

	final, size, err := takeInto(conn, dir, name, q, f.into.Progress)
	if err != nil {
		return err
	}
	if f.into.Landed != nil {
		f.into.Landed(at.From, final, size)
	}
	return nil
}

// handReplace answers a replace and then puts the file where the caller said.
//
// The caller names the version they believe is at that name. What is actually there is weighed
// against it before a byte is sent, so a file that somebody else changed in the meantime is a
// refusal the caller can act on rather than a version silently written over.
func (f *Files) handReplace(conn *wire.Conn, at arch.Session, dir *os.Root, name string, q request) error {
	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReply, reply{Reason: reason}.encode())
	}

	if reason := roomFor(dir, name); reason != "" {
		return refuse(reason)
	}
	if reason := standing(dir, name, q.Sum); reason != "" {
		return refuse(reason)
	}
	if err := conn.WriteFrame(wire.KindReply, reply{OK: true}.encode()); err != nil {
		return err
	}

	size, err := takeOver(conn, dir, name, q, f.into.Progress)
	if err != nil {
		return err
	}
	if f.into.Landed != nil {
		f.into.Landed(at.From, name, size)
	}
	return nil
}

// roomFor says why a name cannot be written to, and nothing at all when it can.
func roomFor(dir *os.Root, name string) string {
	if where := path.Dir(name); where != "." {
		if stat, err := dir.Stat(where); err != nil || !stat.IsDir() {
			return fmt.Sprintf("%s is not a directory here", where)
		}
	}
	if stat, err := dir.Lstat(name); err == nil && stat.IsDir() {
		return fmt.Sprintf("%s is a directory", name)
	}
	return ""
}

// standing says why what is at a name is not the version the caller believes is there.
//
// An empty digest is a caller who believes there is nothing there at all, which is what a machine
// that has never seen the file says. A name holding something that is not a plain file is refused
// whatever the caller believes, because there is no version of a device to compare.
func standing(dir *os.Root, name string, sum []byte) string {
	stat, err := dir.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		if len(sum) == 0 {
			return ""
		}
		return fmt.Sprintf("%s is not here, and you wrote as though it were", name)
	}
	if err != nil {
		return fmt.Sprintf("cannot look at %s: %v", name, unpath(err))
	}
	if !stat.Mode().IsRegular() {
		return fmt.Sprintf("%s is not a file", name)
	}
	if len(sum) == 0 {
		return fmt.Sprintf("%s is here already, and you wrote as though it were not", name)
	}

	held, err := digestOf(dir, name)
	if err != nil {
		return fmt.Sprintf("cannot read %s: %v", name, unpath(err))
	}
	if !bytes.Equal(held, sum) {
		return fmt.Sprintf("%s changed since you last read it", name)
	}
	return ""
}

// digestOf is what a file in a namespace holds, as one number.
func digestOf(dir *os.Root, name string) ([]byte, error) {
	file, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sum := blake3.New(32, nil)
	if _, err := io.Copy(sum, file); err != nil {
		return nil, err
	}
	return sum.Sum(nil), nil
}

// listed reads one directory of a namespace into entries.
func listed(dir *os.Root, name string) ([]Entry, error) {
	at, stat, err := reading(dir, name)
	if err != nil {
		return nil, fmt.Errorf("cannot list it: %v", unpath(err))
	}
	defer at.Close()

	if !stat.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", name)
	}

	// One more than the limit is read, so a directory over it is refused rather than half answered.
	items, err := at.ReadDir(MaxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("cannot list it: %v", unpath(err))
	}
	if len(items) > MaxEntries {
		return nil, fmt.Errorf("it holds more than the %d entries one listing carries: ask for a directory inside it by name, or serve one of them as a namespace of its own", MaxEntries)
	}

	out := make([]Entry, 0, len(items))
	for _, item := range items {
		stat, err := item.Info()
		if err != nil {
			continue
		}
		out = append(out, entryOf(stat))
	}
	return out, nil
}

// reading opens a name inside a namespace and hands back the file and what it turned out to be, so
// what a caller weighs is the open file rather than a name that can move under it.
//
// The open does not wait: a pipe under the name would park the session in the kernel, where no
// deadline reaches it. The descriptor comes back non-blocking, and a regular file goes back to
// blocking on the file itself, where there is nothing left to wait for.
func reading(dir *os.Root, name string) (*os.File, fs.FileInfo, error) {
	file, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if stat.Mode().IsRegular() {
		if err := waiting(file); err != nil {
			file.Close()
			return nil, nil, err
		}
	}
	return file, stat, nil
}

// waiting puts an open file back to blocking reads.
func waiting(file *os.File) error {
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}

	var set error
	if err := raw.Control(func(fd uintptr) { set = syscall.SetNonblock(int(fd), false) }); err != nil {
		return err
	}
	return set
}

// entryOf turns what the filesystem says about something into an entry.
func entryOf(stat fs.FileInfo) Entry {
	return Entry{
		Name: stat.Name(),
		Size: stat.Size(),
		Mode: uint32(stat.Mode().Perm()),
		Dir:  stat.IsDir(),
		At:   stat.ModTime().UnixNano(),
	}
}

// unpath strips the filesystem path out of an error, so a refusal says what went wrong without
// telling the far end where this machine keeps things.
func unpath(err error) error {
	var perr *fs.PathError
	if errors.As(err, &perr) {
		return perr.Err
	}
	return err
}

// clean turns a name a peer sent into a path inside a namespace, or refuses it.
//
// A name arrives from a peer nobody vouches for. What comes back is relative, slash-separated, and
// made of ordinary components: nothing absolute, no volume, no dot-dot, no byte a filesystem cannot
// hold. It is only half the containment — the other half is the open directory it is handed to,
// which resolves one component at a time and leaves the namespace for nothing.
//
// The namespace itself cleans to ".", which the caller decides what to do about.
func clean(rel string) (string, error) {
	if len(rel) > MaxRel {
		return "", fmt.Errorf("that path is %d bytes, over the %d limit", len(rel), MaxRel)
	}
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("that path is not a name")
	}
	if strings.ContainsRune(rel, '\\') {
		return "", fmt.Errorf("%q is not a name: a path here is cut at slashes", rel)
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("%q is an absolute path", rel)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", fmt.Errorf("%q leaves the namespace", rel)
		}
	}

	out := path.Clean(rel)
	if out == ".." || strings.HasPrefix(out, "../") {
		return "", fmt.Errorf("%q leaves the namespace", rel)
	}
	return out, nil
}
