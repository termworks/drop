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

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/wire"
)

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

// takeInto reads one item into a namespace and lands it on a free name, which is what it returns.
//
// Nothing that arrives replaces a file that was on this disk first, and nothing is written through a
// name somebody else laid a link on.
func takeInto(conn *wire.Conn, dir *os.Root, name string, size int64, mode uint32, progress func(string, int64, int64)) (string, int64, error) {
	part := partName(name, size)

	got, err := land(conn, dir, part, path.Base(name), size, mode, progress)
	if err != nil {
		return "", 0, err
	}

	final, err := claim(dir, name)
	if err != nil {
		_ = dir.Remove(part)
		return "", 0, fmt.Errorf("making room for %s: %w", name, err)
	}
	if err := dir.Rename(part, final); err != nil {
		_ = dir.Remove(final)
		return "", 0, fmt.Errorf("renaming %s: %w", part, err)
	}

	if err := conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
		return "", 0, fmt.Errorf("acknowledging %s: %w", final, err)
	}
	return final, got, nil
}

// takeOnto reads one item and lands it on the path this side asked for. The part waits in the
// directory the item lands in, opened through it, so nothing on the way is followed.
func takeOnto(conn *wire.Conn, into, name string, size int64, mode uint32, progress func(string, int64, int64)) error {
	where := filepath.Dir(into)
	dir, err := os.OpenRoot(where)
	if err != nil {
		return fmt.Errorf("opening %s: %w", where, err)
	}
	defer dir.Close()

	final := filepath.Base(into)
	part := partName(final, size)

	if _, err := land(conn, dir, part, name, size, mode, progress); err != nil {
		return err
	}
	if err := dir.Rename(part, final); err != nil {
		_ = dir.Remove(part)
		return fmt.Errorf("renaming %s: %w", part, err)
	}
	return conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode())
}

// land reads one item into a part file and leaves it there, verified, trimmed and closed. What did
// not verify is thrown away and the sender is told why.
func land(conn *wire.Conn, dir *os.Root, part, name string, size int64, mode uint32, progress func(string, int64, int64)) (int64, error) {
	out, err := fresh(dir, part)
	if err != nil {
		return 0, fmt.Errorf("opening %s: %w", part, err)
	}
	defer out.Close()

	// Nothing half-arrived is left lying about under a name the next transfer would find.
	lost := func(err error) (int64, error) {
		_ = dir.Remove(part)
		return 0, err
	}

	got, reason, err := drain(conn, out, part, name, size, progress)
	if err != nil {
		return lost(err)
	}
	if reason != "" {
		_ = conn.WriteFrame(wire.KindAck, wire.Ack{Reason: reason}.Encode())
		return lost(fmt.Errorf("%s: %s", name, reason))
	}

	// The item is the whole of this file: what was hashed is what is kept, and the mode goes on the
	// open file rather than on a name somebody else could be holding by then.
	if err := out.Truncate(got); err != nil {
		return lost(fmt.Errorf("trimming %s: %w", part, err))
	}
	if err := out.Chmod(landing(mode)); err != nil {
		return lost(fmt.Errorf("setting the mode of %s: %w", part, err))
	}
	if err := out.Close(); err != nil {
		return lost(fmt.Errorf("closing %s: %w", part, err))
	}
	return got, nil
}

// drain reads a run of data frames into an open file and weighs what arrived against the count and
// the digest the sender ended with. A reason back means the item is not what was promised.
func drain(conn *wire.Conn, out *os.File, part, name string, size int64, progress func(string, int64, int64)) (int64, string, error) {
	digest := blake3.New(32, nil)
	buf := make([]byte, wire.DataChunk)
	got := int64(0)

	for {
		kind, length, err := conn.ReadHeader()
		if err != nil {
			return 0, "", fmt.Errorf("receiving %s: %w", name, err)
		}

		if kind == wire.KindEnd {
			body := make([]byte, length)
			if err := conn.ReadBody(body, length); err != nil {
				return 0, "", err
			}
			end, err := wire.DecodeEnd(body)
			if err != nil {
				return 0, "", err
			}
			if got != end.Size {
				return 0, fmt.Sprintf("arrived as %d bytes, sender counted %d", got, end.Size), nil
			}
			if !bytes.Equal(digest.Sum(nil), end.Digest) {
				return 0, "arrived corrupted: digest mismatch", nil
			}
			return got, "", nil
		}
		if kind != wire.KindData {
			return 0, "", fmt.Errorf("expected data for %s, got frame kind %d", name, kind)
		}

		if err := conn.ReadBody(buf, length); err != nil {
			return 0, "", err
		}
		if _, err := out.Write(buf[:length]); err != nil {
			return 0, "", fmt.Errorf("writing %s: %w", part, err)
		}
		digest.Write(buf[:length])
		got += int64(length)
		if progress != nil {
			progress(name, got, size)
		}
	}
}

// partName is where an item waits while it arrives: beside where it lands, named after it and its
// length, so what an earlier, different transfer left behind is a different file.
func partName(name string, size int64) string {
	sum := blake3.Sum256(fmt.Appendf(nil, "%s\x00%d", path.Base(name), size))
	return path.Join(path.Dir(name), fmt.Sprintf(".%s.%x.part", path.Base(name), sum[:6]))
}

// fresh makes the part file new. Whatever is at that name goes first, so a link planted on a
// guessable path is unlinked rather than written through, and what comes back was made here.
func fresh(dir *os.Root, part string) (*os.File, error) {
	if err := dir.Remove(part); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return dir.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

// landing is what a received file is allowed to be. The sender's bits are a stranger's opinion, so
// all that survives them is whether this is a program.
func landing(mode uint32) os.FileMode {
	if os.FileMode(mode).Perm()&0o111 != 0 {
		return 0o700
	}
	return 0o600
}

// claim takes a free name for a finished item, numbering it when something is already there.
func claim(dir *os.Root, name string) (string, error) {
	for n := range 1000 {
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
	ext := path.Ext(name)
	return fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), n, ext)
}
