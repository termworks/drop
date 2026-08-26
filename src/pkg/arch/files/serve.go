package files

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/wire"
)

// answer carries out one request. A refusal is a reply, not an error: the session stays open so the
// caller can ask for something else.
func (f *Files) answer(conn *wire.Conn, at arch.Session, cfg Config, q request) error {
	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReply, reply{Reason: reason}.encode())
	}

	// The mount flag is the only thing that permits a write. Nothing here is per-operation.
	if q.Op == opPut || q.Op == opRemove || q.Op == opMkdir || q.Op == opMove {
		if !cfg.Writable {
			return refuse(fmt.Sprintf("%s is read-only", at.Path))
		}
	}

	// Only a listing may name the namespace itself. Nothing else is allowed to reach for the
	// directory the mount stands on.
	if q.Name == "" && q.Op != opList {
		return refuse("that is the namespace itself")
	}

	full, err := under(cfg.Dir, q.Name)
	if err != nil {
		return refuse(err.Error())
	}

	switch q.Op {
	case opList:
		entries, err := listed(full)
		if err != nil {
			return refuse(err.Error())
		}
		return conn.WriteFrame(wire.KindReply, reply{OK: true, Entries: entries}.encode())

	case opGet:
		return f.handGet(conn, full, q)

	case opPut:
		return f.handPut(conn, at, full, q)

	case opRemove:
		if err := os.Remove(full); err != nil {
			return refuse(fmt.Sprintf("cannot remove %s: %v", q.Name, unpath(err)))
		}
		return conn.WriteFrame(wire.KindReply, reply{OK: true}.encode())

	case opMkdir:
		if err := os.Mkdir(full, 0o700); err != nil {
			return refuse(fmt.Sprintf("cannot make %s: %v", q.Name, unpath(err)))
		}
		return conn.WriteFrame(wire.KindReply, reply{OK: true}.encode())

	case opMove:
		to, err := under(cfg.Dir, q.To)
		if err != nil {
			return refuse(err.Error())
		}
		if err := os.Rename(full, to); err != nil {
			return refuse(fmt.Sprintf("cannot move %s: %v", q.Name, unpath(err)))
		}
		return conn.WriteFrame(wire.KindReply, reply{OK: true}.encode())

	default:
		return refuse(fmt.Sprintf("operation %d is not one this serves", q.Op))
	}
}

// handGet answers a get and then writes the file out.
func (f *Files) handGet(conn *wire.Conn, full string, q request) error {
	stat, err := os.Stat(full)
	if err != nil {
		return conn.WriteFrame(wire.KindReply, reply{Reason: fmt.Sprintf("cannot read %s: %v", q.Name, unpath(err))}.encode())
	}
	if stat.IsDir() {
		return conn.WriteFrame(wire.KindReply, reply{Reason: fmt.Sprintf("%s is a directory", q.Name)}.encode())
	}

	file, err := os.Open(full)
	if err != nil {
		return conn.WriteFrame(wire.KindReply, reply{Reason: fmt.Sprintf("cannot read %s: %v", q.Name, unpath(err))}.encode())
	}
	defer file.Close()

	said := reply{OK: true, Entries: []Entry{entryOf(stat)}}
	if err := conn.WriteFrame(wire.KindReply, said.encode()); err != nil {
		return err
	}
	return sendBody(conn, file, filepath.Base(full), stat.Size(), f.into.Progress)
}

// handPut answers a put and then takes the file in.
func (f *Files) handPut(conn *wire.Conn, at arch.Session, full string, q request) error {
	if stat, err := os.Stat(full); err == nil && stat.IsDir() {
		return conn.WriteFrame(wire.KindReply, reply{Reason: fmt.Sprintf("%s is a directory", q.Name)}.encode())
	}
	if err := conn.WriteFrame(wire.KindReply, reply{OK: true}.encode()); err != nil {
		return err
	}

	name := filepath.Base(full)
	if err := takeBody(conn, name, full, q.Size, q.Mode, f.into.Progress); err != nil {
		return err
	}
	if f.into.Landed != nil {
		if stat, err := os.Stat(full); err == nil {
			f.into.Landed(at.From, name, stat.Size())
		}
	}
	return nil
}

// listed reads one directory into entries.
func listed(dir string) ([]Entry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot list it: %v", unpath(err))
	}
	if len(items) > MaxEntries {
		return nil, fmt.Errorf("it holds %d entries, more than the %d one listing carries", len(items), MaxEntries)
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

// sendBody writes a run of data frames, ends it with a size and a digest, and waits for the verdict.
func sendBody(conn *wire.Conn, body io.Reader, name string, size int64, progress func(string, int64, int64)) error {
	digest := blake3.New(32, nil)
	buf := make([]byte, wire.DataChunk)
	sent := int64(0)

	for {
		n, err := body.Read(buf)
		if n > 0 {
			if werr := conn.WriteData(buf[:n]); werr != nil {
				return fmt.Errorf("sending %s: %w", name, werr)
			}
			digest.Write(buf[:n])
			sent += int64(n)
			if progress != nil {
				progress(name, sent, size)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
	}

	end := wire.End{Size: sent, Digest: digest.Sum(nil)}
	if err := conn.WriteFrame(wire.KindEnd, end.Encode()); err != nil {
		return err
	}

	kind, ackBody, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("waiting for %s to be confirmed: %w", name, err)
	}
	if kind != wire.KindAck {
		return fmt.Errorf("expected an ack for %s, got frame kind %d", name, kind)
	}
	ack, err := wire.DecodeAck(ackBody)
	if err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("%s was rejected: %s", name, ack.Reason)
	}
	return nil
}

// takeBody reads a run of data frames into a path, checks the digest, and says what it found.
//
// The bytes land beside the destination and are renamed onto it, so a transfer that fails leaves
// what was there alone. Permission comes from this machine: what arrives is readable by whoever
// runs it and nobody else, and the sender is trusted for one bit, whether the thing runs.
func takeBody(conn *wire.Conn, name, into string, size int64, mode uint32, progress func(string, int64, int64)) error {
	part := into + ".part"
	out, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", part, err)
	}
	defer out.Close()

	refuse := func(reason string) error {
		os.Remove(part)
		_ = conn.WriteFrame(wire.KindAck, wire.Ack{Reason: reason}.Encode())
		return fmt.Errorf("%s: %s", name, reason)
	}

	digest := blake3.New(32, nil)
	buf := make([]byte, wire.DataChunk)
	got := int64(0)

	for {
		kind, length, err := conn.ReadHeader()
		if err != nil {
			return fmt.Errorf("receiving %s: %w", name, err)
		}

		if kind == wire.KindEnd {
			endBody := make([]byte, length)
			if err := conn.ReadBody(endBody, length); err != nil {
				return err
			}
			end, err := wire.DecodeEnd(endBody)
			if err != nil {
				return err
			}
			if got != end.Size {
				return refuse(fmt.Sprintf("arrived as %d bytes, sender counted %d", got, end.Size))
			}
			if !bytes.Equal(digest.Sum(nil), end.Digest) {
				return refuse("arrived corrupted: digest mismatch")
			}
			break
		}
		if kind != wire.KindData {
			return fmt.Errorf("expected data for %s, got frame kind %d", name, kind)
		}

		if err := conn.ReadBody(buf, length); err != nil {
			return err
		}
		if _, err := out.Write(buf[:length]); err != nil {
			return fmt.Errorf("writing %s: %w", part, err)
		}
		digest.Write(buf[:length])
		got += int64(length)
		if progress != nil {
			progress(name, got, size)
		}
	}

	perm := os.FileMode(0o600)
	if mode&0o111 != 0 {
		perm |= 0o100
	}
	if err := out.Chmod(perm); err != nil {
		return fmt.Errorf("setting the mode of %s: %w", part, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", part, err)
	}
	if err := os.Rename(part, into); err != nil {
		return fmt.Errorf("renaming %s: %w", part, err)
	}
	return conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode())
}

// under joins a relative path to a namespace's directory, and refuses anything that would leave it.
//
// A name arrives from a peer nobody vouches for and becomes a path on this disk. Absolute names,
// dot-dot, and anything over the length bound are refused outright; the rest is resolved through
// symlinks — the root, and the target or the nearest part of it that exists — and refused unless
// what comes back is still inside the root.
func under(root, rel string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("this namespace has no directory")
	}
	if len(rel) > MaxRel {
		return "", fmt.Errorf("that path is %d bytes, over the %d limit", len(rel), MaxRel)
	}
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("that path is not a name")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%q is an absolute path", rel)
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return "", fmt.Errorf("%q leaves the namespace", rel)
		}
	}

	// The root is resolved once, because it is what everything else is measured against.
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("this namespace's directory cannot be resolved: %v", unpath(err))
	}

	full := filepath.Join(base, filepath.Clean(rel))
	found, err := settled(full)
	if err != nil {
		return "", err
	}
	if !inside(base, found) {
		return "", fmt.Errorf("%q leaves the namespace", rel)
	}
	return full, nil
}

// settled resolves a path through symlinks, walking up to the nearest part of it that exists when
// the path itself does not. What is missing cannot be a link, so what is found decides.
func settled(full string) (string, error) {
	at, rest := full, ""

	for {
		found, err := filepath.EvalSymlinks(at)
		if err == nil {
			return filepath.Join(found, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("that path cannot be resolved: %v", unpath(err))
		}

		parent := filepath.Dir(at)
		if parent == at {
			return "", fmt.Errorf("that path cannot be resolved: %v", unpath(err))
		}
		rest = filepath.Join(filepath.Base(at), rest)
		at = parent
	}
}

// inside reports whether a resolved path is the root or below it.
func inside(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
