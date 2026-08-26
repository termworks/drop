package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

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
	if q.Op == opPut || q.Op == opRemove || q.Op == opMkdir || q.Op == opMove {
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
		return conn.WriteFrame(wire.KindReply, reply{OK: true, Entries: entries}.encode())

	case opGet:
		return f.handGet(conn, dir, name)

	case opPut:
		return f.handPut(conn, at, dir, name, q)

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

// handGet answers a get and then writes the file out.
func (f *Files) handGet(conn *wire.Conn, dir *os.Root, name string) error {
	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReply, reply{Reason: reason}.encode())
	}

	// A get is a regular file. A directory, a device or a pipe is not something to read down a
	// stream, and what was opened is looked at again in case the name moved under it.
	stat, err := dir.Stat(name)
	if err != nil {
		return refuse(fmt.Sprintf("cannot read %s: %v", name, unpath(err)))
	}
	if stat.IsDir() {
		return refuse(fmt.Sprintf("%s is a directory", name))
	}
	if !stat.Mode().IsRegular() {
		return refuse(fmt.Sprintf("%s is not a file", name))
	}

	file, err := dir.Open(name)
	if err != nil {
		return refuse(fmt.Sprintf("cannot read %s: %v", name, unpath(err)))
	}
	defer file.Close()

	open, err := file.Stat()
	if err != nil || !open.Mode().IsRegular() {
		return refuse(fmt.Sprintf("%s is not a file", name))
	}

	said := reply{OK: true, Entries: []Entry{entryOf(open)}}
	if err := conn.WriteFrame(wire.KindReply, said.encode()); err != nil {
		return err
	}
	return sendBody(conn, file, path.Base(name), open.Size(), f.into.Progress)
}

// handPut answers a put and then takes the file in.
func (f *Files) handPut(conn *wire.Conn, at arch.Session, dir *os.Root, name string, q request) error {
	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReply, reply{Reason: reason}.encode())
	}

	// What cannot be taken is refused before the caller starts sending, so a put that has nowhere to
	// land ends the round rather than the session.
	if where := path.Dir(name); where != "." {
		if stat, err := dir.Stat(where); err != nil || !stat.IsDir() {
			return refuse(fmt.Sprintf("%s is not a directory here", where))
		}
	}
	if stat, err := dir.Lstat(name); err == nil && stat.IsDir() {
		return refuse(fmt.Sprintf("%s is a directory", name))
	}
	if err := conn.WriteFrame(wire.KindReply, reply{OK: true}.encode()); err != nil {
		return err
	}

	final, size, err := takeInto(conn, dir, name, q.Size, q.Mode, f.into.Progress)
	if err != nil {
		return err
	}
	if f.into.Landed != nil {
		f.into.Landed(at.From, final, size)
	}
	return nil
}

// listed reads one directory of a namespace into entries.
func listed(dir *os.Root, name string) ([]Entry, error) {
	stat, err := dir.Stat(name)
	if err != nil {
		return nil, fmt.Errorf("cannot list it: %v", unpath(err))
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", name)
	}

	at, err := dir.Open(name)
	if err != nil {
		return nil, fmt.Errorf("cannot list it: %v", unpath(err))
	}
	defer at.Close()

	// One more than the limit is read, so a directory over it is refused rather than half answered.
	items, err := at.ReadDir(MaxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("cannot list it: %v", unpath(err))
	}
	if len(items) > MaxEntries {
		return nil, fmt.Errorf("it holds more than the %d entries one listing carries", MaxEntries)
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

// entryOf turns what the filesystem says about something into an entry.
func entryOf(stat fs.FileInfo) Entry {
	return Entry{
		Name: stat.Name(),
		Size: stat.Size(),
		Mode: uint32(stat.Mode().Perm()),
		Dir:  stat.IsDir(),
		At:   stat.ModTime().Unix(),
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
