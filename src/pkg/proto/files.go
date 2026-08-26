package proto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Source is one thing to send. Reader is used when set, which is how something with no length —
// stdin, a pipe — is sent; otherwise Path is opened.
type Source struct {
	Name   string
	Path   string
	Size   int64
	Mode   uint32
	Reader io.Reader
}

// Known reports whether this source's length was settled before sending.
func (s Source) Known() bool {
	return s.Size >= 0
}

// FileFromPath describes a file on disk, whose size is known.
func FileFromPath(path string) (Source, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return Source{}, fmt.Errorf("cannot send %s: %w", path, err)
	}
	if stat.IsDir() {
		return Source{}, fmt.Errorf("cannot send %s: directories are not supported yet", path)
	}
	return Source{
		Name: filepath.Base(path),
		Path: path,
		Size: stat.Size(),
		Mode: uint32(stat.Mode().Perm()),
	}, nil
}

// FileFromReader describes something whose length is not known until it ends.
func FileFromReader(name string, r io.Reader) Source {
	return Source{Name: name, Size: SizeUnknown, Mode: 0o644, Reader: r}
}

// safeName strips any directory part a sender put in a name, so an offer cannot write outside the
// receiving directory.
func safeName(name string) string {
	clean := filepath.Base(filepath.Clean("/" + name))
	if clean == "/" || clean == "." || clean == ".." {
		return ""
	}
	return clean
}

// SendFiles offers sources to a peer and writes the ones it accepts.
func SendFiles(ctx context.Context, s io.ReadWriteCloser, path string, sources []Source, from string, progress func(string, int64, int64)) error {

	conn := wire.NewConn(s)

	open := Open{Mode: ModeShare, From: from, Path: path}
	open.Badge, open.Signed = carried()
	for _, src := range sources {
		open.Items = append(open.Items, Item{Name: src.Name, Size: src.Size, Mode: src.Mode})
	}
	if err := conn.WriteFrame(wire.KindOpen, open.encode()); err != nil {
		return err
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("reading the answer: %w", err)
	}
	switch kind {
	case wire.KindAccept:
	case wire.KindReject:
		reject, err := decodeReject(body)
		if err != nil {
			return err
		}
		return Declined{Reason: reject.Reason}
	default:
		return fmt.Errorf("expected an answer, got frame kind %d", kind)
	}

	accept, err := decodeAccept(body)
	if err != nil {
		return err
	}

	for i, src := range sources {
		var resume int64
		if i < len(accept.Resume) {
			resume = accept.Resume[i]
		}
		if err := sendOne(conn, src, resume, progress); err != nil {
			return err
		}
	}
	return nil
}

func sendOne(conn *wire.Conn, src Source, resume int64, progress func(string, int64, int64)) error {
	body := src.Reader
	if body == nil {
		file, err := os.Open(src.Path)
		if err != nil {
			return fmt.Errorf("opening %s: %w", src.Path, err)
		}
		defer file.Close()
		body = file
	}

	digest := blake3.New(32, nil)

	// The digest covers the whole item, so a resumed prefix is hashed without being sent. Only a
	// known-size item can be resumed; there is nothing to seek in a pipe.
	if resume > 0 && src.Known() {
		if _, err := io.CopyN(digest, body, resume); err != nil {
			return fmt.Errorf("hashing the resumed part of %s: %w", src.Name, err)
		}
	}

	sent := resume
	buf := make([]byte, wire.DataChunk)

	for {
		n, err := body.Read(buf)
		if n > 0 {
			if werr := conn.WriteData(buf[:n]); werr != nil {
				return fmt.Errorf("sending %s: %w", src.Name, werr)
			}
			digest.Write(buf[:n])
			sent += int64(n)
			if progress != nil {
				progress(src.Name, sent, src.Size)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", src.Name, err)
		}
	}

	if src.Known() && sent != src.Size {
		return fmt.Errorf("%s changed size while being sent: %d bytes, expected %d", src.Name, sent, src.Size)
	}

	end := End{Size: sent, Digest: digest.Sum(nil)}
	if err := conn.WriteFrame(wire.KindEnd, end.encode()); err != nil {
		return err
	}

	// Waiting on the verdict is what makes a successful return mean the bytes were verified on the
	// other side, rather than only that they were written to a socket.
	kind, ackBody, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("waiting for %s to be confirmed: %w", src.Name, err)
	}
	if kind != wire.KindAck {
		return fmt.Errorf("expected an ack for %s, got frame kind %d", src.Name, kind)
	}
	ack, err := decodeAck(ackBody)
	if err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("%s was rejected: %s", src.Name, ack.Reason)
	}
	return nil
}

// partName is where an item waits while it arrives. The name and the length are folded into it, so
// what an earlier, different offer left behind is a different file and is never resumed against.
func partName(item Item) string {
	name := safeName(item.Name)
	sum := blake3.Sum256(fmt.Appendf(nil, "%s\x00%d", name, item.Size))
	return fmt.Sprintf(".%s.%x.part", name, sum[:6])
}

// offered reads an offer before anything is made for it. Two items on one name means the second
// landing on top of the first, which nobody asked for.
func offered(items []Item) error {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		name := safeName(item.Name)
		if seen[name] {
			return fmt.Errorf("%s was offered twice", name)
		}
		seen[name] = true
	}
	return nil
}

// receiveFiles takes the items of an accepted files session.
func receiveFiles(conn *wire.Conn, policy Policy, from node.ID, open Open) error {
	if err := offered(open.Items); err != nil {
		_ = conn.WriteFrame(wire.KindReject, Reject{Reason: err.Error()}.encode())
		return err
	}
	if err := os.MkdirAll(policy.Dir, 0o700); err != nil {
		_ = conn.WriteFrame(wire.KindReject, Reject{Reason: "cannot write here"}.encode())
		return fmt.Errorf("creating %s: %w", policy.Dir, err)
	}

	// Everything below happens through the open directory, which follows no symlink out of it: the
	// paths here are guessable, and the machine this runs on may have other people on it.
	dir, err := os.OpenRoot(policy.Dir)
	if err != nil {
		_ = conn.WriteFrame(wire.KindReject, Reject{Reason: "cannot write here"}.encode())
		return fmt.Errorf("opening %s: %w", policy.Dir, err)
	}
	defer dir.Close()

	accept := Accept{Resume: make([]int64, len(open.Items))}
	for i, item := range open.Items {
		// Only a known-size item can be resumed: without a length there is no way to tell a partial
		// file from a complete one.
		if !item.Known() {
			continue
		}
		stat, err := dir.Lstat(partName(item))
		if err == nil && stat.Mode().IsRegular() && stat.Size() <= item.Size {
			accept.Resume[i] = stat.Size()
		}
	}
	if err := conn.WriteFrame(wire.KindAccept, accept.encode()); err != nil {
		return err
	}

	for i, item := range open.Items {
		if err := receiveOne(conn, policy, dir, from, item, accept.Resume[i]); err != nil {
			return err
		}
	}

	if policy.Finished != nil {
		policy.Finished(from, len(open.Items))
	}
	return nil
}

func receiveOne(conn *wire.Conn, policy Policy, dir *os.Root, from node.ID, item Item, resume int64) error {
	name := safeName(item.Name)
	part := partName(item)

	// Truncating unless this is picking something up: a longer tail from an abandoned transfer would
	// otherwise stay under what arrives now, and be part of the file nobody hashed.
	flags := os.O_CREATE | os.O_RDWR
	if resume == 0 {
		flags |= os.O_TRUNC
	}
	out, err := dir.OpenFile(part, flags, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", part, err)
	}
	defer out.Close()

	digest := blake3.New(32, nil)
	if resume > 0 {
		if _, err := io.CopyN(digest, io.NewSectionReader(out, 0, resume), resume); err != nil {
			return fmt.Errorf("rehashing %s: %w", part, err)
		}
	}

	if _, err := out.Seek(resume, io.SeekStart); err != nil {
		return fmt.Errorf("seeking in %s: %w", part, err)
	}

	// Data frames run until the item ends, which is what lets an item arrive whose length nobody
	// knew when it started.
	got := resume
	buf := make([]byte, wire.DataChunk)

	for {
		kind, size, err := conn.ReadHeader()
		if err != nil {
			return fmt.Errorf("receiving %s: %w", name, err)
		}

		if kind == wire.KindEnd {
			endBody := make([]byte, size)
			if err := conn.ReadBody(endBody, size); err != nil {
				return err
			}
			end, err := decodeEnd(endBody)
			if err != nil {
				return err
			}
			return finishOne(conn, policy, dir, from, item, name, part, out, digest, got, end)
		}
		if kind != wire.KindData {
			return fmt.Errorf("expected data for %s, got frame kind %d", name, kind)
		}

		if err := conn.ReadBody(buf, size); err != nil {
			return err
		}
		if _, err := out.Write(buf[:size]); err != nil {
			return fmt.Errorf("writing %s: %w", part, err)
		}
		digest.Write(buf[:size])
		got += int64(size)
		if policy.Progress != nil {
			policy.Progress(name, got, item.Size)
		}
	}
}

func finishOne(conn *wire.Conn, policy Policy, dir *os.Root, from node.ID, item Item, name, part string, out *os.File, digest *blake3.Hasher, got int64, end End) error {
	refuse := func(reason string) error {
		_ = dir.Remove(part)
		_ = conn.WriteFrame(wire.KindAck, Ack{Reason: reason}.encode())
		return fmt.Errorf("%s: %s", name, reason)
	}

	if got != end.Size {
		return refuse(fmt.Sprintf("arrived as %d bytes, sender counted %d", got, end.Size))
	}
	if !bytes.Equal(digest.Sum(nil), end.Digest) {
		return refuse("arrived corrupted: digest mismatch")
	}

	// The item is the whole of this file: what was hashed is what is kept, and the mode goes on the
	// open file rather than on a name somebody else could be holding by then.
	if err := out.Truncate(got); err != nil {
		return fmt.Errorf("trimming %s: %w", part, err)
	}
	if err := out.Chmod(landing(item.Mode)); err != nil {
		return fmt.Errorf("setting the mode of %s: %w", part, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", part, err)
	}

	final, err := claim(dir, name)
	if err != nil {
		return fmt.Errorf("making room for %s: %w", name, err)
	}
	if err := dir.Rename(part, final); err != nil {
		_ = dir.Remove(final)
		return fmt.Errorf("renaming %s: %w", part, err)
	}

	if err := conn.WriteFrame(wire.KindAck, Ack{OK: true}.encode()); err != nil {
		return fmt.Errorf("acknowledging %s: %w", final, err)
	}
	if policy.Done != nil {
		policy.Done(from, final, got)
	}
	return nil
}

// landing is what a received file is allowed to be. The sender's bits are a stranger's opinion, so
// all that survives them is whether this is a program.
func landing(mode uint32) os.FileMode {
	if os.FileMode(mode).Perm()&0o111 != 0 {
		return 0o700
	}
	return 0o600
}

// claim takes a free name for a finished item, numbering it when something is already there. What
// arrives over the wire never replaces a file that was on this disk first.
func claim(dir *os.Root, name string) (string, error) {
	for n := 0; n < 1000; n++ {
		at := numbered(name, n)
		f, err := dir.OpenFile(at, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return at, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("%s and the thousand names after it are taken", name)
}

// numbered spaces a name out: report.txt, report-1.txt, report-2.txt.
func numbered(name string, n int) string {
	if n == 0 {
		return name
	}
	ext := filepath.Ext(name)
	return fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), n, ext)
}
