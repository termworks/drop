package proto

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

	"github.com/bresilla/drop/src/pkg/wire"
)

// Walking a directory somebody else is serving.
//
// One stream carries one Open and then as many request and reply rounds as the caller wants: list a
// directory, read a file out of it, read another, write one back. A round is a KindRequest frame
// and a KindReply frame, and the two transfer operations put a run of data frames between them,
// ending the way every transfer in drop ends — a size, a digest, and a verdict.

// Operations a browse session can ask for.
const (
	opList   byte = 1
	opGet    byte = 2
	opPut    byte = 3
	opRemove byte = 4
	opMkdir  byte = 5
	opMove   byte = 6
)

// MaxRel bounds a path named inside a namespace. It arrives from a peer and becomes a filesystem
// path, so it is measured before it is looked at.
const MaxRel = 1024

// MaxEntries caps one listing, so a directory cannot be answered with an unbounded slice.
const MaxEntries = 1 << 14

// Entry is one thing in a served directory.
type Entry struct {
	Name string
	Size int64
	Mode uint32
	Dir  bool
	// At is when it last changed, in seconds.
	At int64
}

// ready answers the Open of a files namespace: what the far end will let the caller do in it.
type ready struct {
	Writable bool
}

func (y ready) encode() []byte {
	w := wire.NewWriter()
	w.Bool(y.Writable)
	return w.Body()
}

func decodeReady(body []byte) (ready, error) {
	r := wire.NewReader(body)
	writable, err := r.Bool()
	return ready{Writable: writable}, err
}

// request is one operation on a files namespace. Size and Mode describe an upload; To is the
// destination of a move. The rest of the time they are zero.
type request struct {
	Op   byte
	Name string
	To   string
	Size int64
	Mode uint32
}

func (q request) encode() []byte {
	w := wire.NewWriter()
	w.Byte(q.Op)
	w.String(q.Name)
	w.String(q.To)
	w.Int(q.Size)
	w.Uint(uint64(q.Mode))
	return w.Body()
}

func decodeRequest(body []byte) (request, error) {
	var out request

	r := wire.NewReader(body)
	op, err := r.Byte()
	if err != nil {
		return out, err
	}
	name, err := r.String(MaxRel)
	if err != nil {
		return out, err
	}
	to, err := r.String(MaxRel)
	if err != nil {
		return out, err
	}
	size, err := r.Int()
	if err != nil {
		return out, err
	}
	mode, err := r.Uint()
	if err != nil {
		return out, err
	}
	out.Op, out.Name, out.To, out.Size, out.Mode = op, name, to, size, uint32(mode)
	return out, nil
}

// reply is what came of one request. Entries carries a listing, or the one file a get is about to
// send.
type reply struct {
	OK      bool
	Reason  string
	Entries []Entry
}

func (p reply) encode() []byte {
	w := wire.NewWriter()
	w.Bool(p.OK)
	w.String(p.Reason)
	w.Uint(uint64(len(p.Entries)))
	for _, e := range p.Entries {
		w.String(e.Name)
		w.Int(e.Size)
		w.Uint(uint64(e.Mode))
		w.Bool(e.Dir)
		w.Int(e.At)
	}
	return w.Body()
}

func decodeReply(body []byte) (reply, error) {
	var out reply

	r := wire.NewReader(body)
	ok, err := r.Bool()
	if err != nil {
		return out, err
	}
	reason, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	count, err := r.Uint()
	if err != nil {
		return out, err
	}
	if count > MaxEntries {
		return out, fmt.Errorf("a listing claims %d entries, over the %d limit", count, MaxEntries)
	}

	out.OK, out.Reason = ok, reason
	out.Entries = make([]Entry, 0, hint(count, body, 5))
	for range count {
		name, err := r.String(MaxRel)
		if err != nil {
			return out, err
		}
		size, err := r.Int()
		if err != nil {
			return out, err
		}
		mode, err := r.Uint()
		if err != nil {
			return out, err
		}
		dir, err := r.Bool()
		if err != nil {
			return out, err
		}
		at, err := r.Int()
		if err != nil {
			return out, err
		}
		out.Entries = append(out.Entries, Entry{
			Name: name,
			Size: size,
			Mode: uint32(mode),
			Dir:  dir,
			At:   at,
		})
	}
	return out, nil
}

// Browsing is an open files namespace on another device.
type Browsing struct {
	conn     *wire.Conn
	stream   io.ReadWriteCloser
	writable bool
}

// Browse opens a files namespace on another device.
func Browse(s io.ReadWriteCloser, path, from string) (*Browsing, error) {
	conn := wire.NewConn(s)

	open := Open{Mode: ModeFiles, From: from, Path: path}
	open.Badge, open.Signed = carried()
	if err := conn.WriteFrame(wire.KindOpen, open.encode()); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("reading the answer: %w", err)
	}
	switch kind {
	case wire.KindAccept:
		y, err := decodeReady(body)
		if err != nil {
			_ = s.Close()
			return nil, err
		}
		return &Browsing{conn: conn, stream: s, writable: y.Writable}, nil
	case wire.KindReject:
		reject, derr := decodeReject(body)
		_ = s.Close()
		if derr != nil {
			return nil, derr
		}
		return nil, Declined{Reason: reject.Reason}
	default:
		_ = s.Close()
		return nil, fmt.Errorf("expected an answer, got frame kind %d", kind)
	}
}

// Writable reports whether this namespace takes anything back.
func (b *Browsing) Writable() bool {
	return b.writable
}

// Close ends the session.
func (b *Browsing) Close() error {
	return b.stream.Close()
}

// round sends one request and reads the reply.
func (b *Browsing) round(q request) (reply, error) {
	if err := b.conn.WriteFrame(wire.KindRequest, q.encode()); err != nil {
		return reply{}, fmt.Errorf("asking about %s: %w", q.Name, err)
	}

	kind, body, err := b.conn.ReadFrame()
	if err != nil {
		return reply{}, fmt.Errorf("reading the answer about %s: %w", q.Name, err)
	}
	if kind != wire.KindReply {
		return reply{}, fmt.Errorf("expected a reply, got frame kind %d", kind)
	}
	return decodeReply(body)
}

// List names what is in one directory of the namespace. The empty name is its root.
func (b *Browsing) List(dir string) ([]Entry, error) {
	got, err := b.round(request{Op: opList, Name: dir})
	if err != nil {
		return nil, err
	}
	if !got.OK {
		return nil, fmt.Errorf("listing %s: %s", shown(dir), got.Reason)
	}
	return got.Entries, nil
}

// Get reads one file out of the namespace and writes it to a local path.
func (b *Browsing) Get(name, into string, progress func(name string, done, total int64)) error {
	got, err := b.round(request{Op: opGet, Name: name})
	if err != nil {
		return err
	}
	if !got.OK {
		return fmt.Errorf("reading %s: %s", shown(name), got.Reason)
	}

	size, mode := SizeUnknown, uint32(0)
	if len(got.Entries) > 0 {
		size, mode = got.Entries[0].Size, got.Entries[0].Mode
	}
	return takeBody(b.conn, filepath.Base(name), into, size, mode, progress)
}

// Put writes one file into the namespace. It fails unless the far end said the namespace is
// writable.
func (b *Browsing) Put(name string, src Source, progress func(name string, done, total int64)) error {
	body := src.Reader
	if body == nil {
		file, err := os.Open(src.Path)
		if err != nil {
			return fmt.Errorf("opening %s: %w", src.Path, err)
		}
		defer file.Close()
		body = file
	}

	got, err := b.round(request{Op: opPut, Name: name, Size: src.Size, Mode: src.Mode})
	if err != nil {
		return err
	}
	if !got.OK {
		return fmt.Errorf("writing %s: %s", shown(name), got.Reason)
	}
	return sendBody(b.conn, body, filepath.Base(name), src.Size, progress)
}

// Remove deletes one file, or one empty directory.
func (b *Browsing) Remove(name string) error {
	return b.did(request{Op: opRemove, Name: name}, "removing")
}

// Mkdir makes one directory.
func (b *Browsing) Mkdir(name string) error {
	return b.did(request{Op: opMkdir, Name: name}, "making")
}

// Move renames something inside the namespace.
func (b *Browsing) Move(from, to string) error {
	return b.did(request{Op: opMove, Name: from, To: to}, "moving")
}

// did runs a round that answers with nothing but a verdict.
func (b *Browsing) did(q request, doing string) error {
	got, err := b.round(q)
	if err != nil {
		return err
	}
	if !got.OK {
		return fmt.Errorf("%s %s: %s", doing, shown(q.Name), got.Reason)
	}
	return nil
}

// shown names a path in an error, for the root of a namespace as well as a file in it.
func shown(name string) string {
	if name == "" {
		return "the namespace"
	}
	return name
}

// serveFiles answers rounds on a files namespace until the caller stops asking.
func serveFiles(conn *wire.Conn, at Resolved, policy Policy) error {
	if at.Mount.Dir == "" {
		return conn.WriteFrame(wire.KindReject, Reject{Reason: "this namespace has no directory"}.encode())
	}
	if err := conn.WriteFrame(wire.KindAccept, ready{Writable: at.Mount.Writable}.encode()); err != nil {
		return err
	}

	for {
		kind, body, err := conn.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("reading a request for %s: %w", at.Mount.Path, err)
		}
		if kind != wire.KindRequest {
			return fmt.Errorf("expected a request, got frame kind %d", kind)
		}

		q, err := decodeRequest(body)
		if err != nil {
			return err
		}
		if err := answer(conn, at, policy, q); err != nil {
			return err
		}
	}
}

// answer carries out one request. A refusal is a reply, not an error: the session stays open so the
// caller can ask for something else.
func answer(conn *wire.Conn, at Resolved, policy Policy, q request) error {
	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReply, reply{Reason: reason}.encode())
	}

	// The mount flag is the only thing that permits a write. Nothing here is per-operation.
	if q.Op == opPut || q.Op == opRemove || q.Op == opMkdir || q.Op == opMove {
		if !at.Mount.Writable {
			return refuse(fmt.Sprintf("%s is read-only", at.Mount.Path))
		}
	}

	// Only a listing may name the namespace itself. Nothing else is allowed to reach for the
	// directory the mount stands on.
	if q.Name == "" && q.Op != opList {
		return refuse("that is the namespace itself")
	}

	full, err := under(at.Mount.Dir, q.Name)
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
		return handGet(conn, policy, full, q)

	case opPut:
		return handPut(conn, at, policy, full, q)

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
		to, err := under(at.Mount.Dir, q.To)
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
func handGet(conn *wire.Conn, policy Policy, full string, q request) error {
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
	return sendBody(conn, file, filepath.Base(full), stat.Size(), policy.Progress)
}

// handPut answers a put and then takes the file in.
func handPut(conn *wire.Conn, at Resolved, policy Policy, full string, q request) error {
	if stat, err := os.Stat(full); err == nil && stat.IsDir() {
		return conn.WriteFrame(wire.KindReply, reply{Reason: fmt.Sprintf("%s is a directory", q.Name)}.encode())
	}
	if err := conn.WriteFrame(wire.KindReply, reply{OK: true}.encode()); err != nil {
		return err
	}

	name := filepath.Base(full)
	if err := takeBody(conn, name, full, q.Size, q.Mode, policy.Progress); err != nil {
		return err
	}
	if policy.Done != nil {
		if stat, err := os.Stat(full); err == nil {
			policy.Done(at.From, name, stat.Size())
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

	end := End{Size: sent, Digest: digest.Sum(nil)}
	if err := conn.WriteFrame(wire.KindEnd, end.encode()); err != nil {
		return err
	}

	kind, ackBody, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("waiting for %s to be confirmed: %w", name, err)
	}
	if kind != wire.KindAck {
		return fmt.Errorf("expected an ack for %s, got frame kind %d", name, kind)
	}
	ack, err := decodeAck(ackBody)
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
		_ = conn.WriteFrame(wire.KindAck, Ack{Reason: reason}.encode())
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
			end, err := decodeEnd(endBody)
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
	return conn.WriteFrame(wire.KindAck, Ack{OK: true}.encode())
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
