package files

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/wire"
)

// sendBody writes a run of data frames, ends it with a size and a digest, and waits for the verdict.
//
// from is how much of the item the far end already holds. Everything is read and weighed, so the
// size and the digest are the whole item's, and only what the far end is missing goes on the wire.
func sendBody(conn *wire.Conn, body io.Reader, name string, size, from int64, progress func(string, int64, int64)) error {
	digest := blake3.New(32, nil)
	buf := make([]byte, wire.DataChunk)
	read := int64(0)

	for {
		n, err := body.Read(buf)
		if n > 0 {
			chunk, start := buf[:n], read
			digest.Write(chunk)
			read += int64(n)

			if read > from {
				if start < from {
					chunk = chunk[from-start:]
				}
				if werr := conn.WriteData(chunk); werr != nil {
					return fmt.Errorf("sending %s: %w", name, werr)
				}
			}
			if progress != nil {
				progress(name, read, size)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
	}

	end := wire.End{Size: read, Digest: digest.Sum(nil)}
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

// arriving is a part file being filled: where it waits, how much of the item is already in it, and
// whether it is worth keeping when something goes wrong.
type arriving struct {
	part string
	have int64
	// kept says the part is named after the digest of what is coming, so a transfer that is cut
	// off leaves it where the next attempt for the same bytes will find it.
	kept bool
}

// takeInto reads one item into a namespace and lands it on a free name, which is what it returns.
//
// Nothing that arrives replaces a file that was on this disk first, nothing is written through a
// name somebody else laid a link on, and the part this one fills is nobody else's.
func takeInto(conn *wire.Conn, dir *os.Root, name string, q request, progress func(string, int64, int64)) (string, int64, error) {
	part, err := partName(name)
	if err != nil {
		return "", 0, err
	}

	got, err := land(conn, dir, arriving{part: part}, path.Base(name), q.Size, q.Mode, progress)
	if err != nil {
		return "", 0, err
	}

	final, err := place(dir, part, name)
	if err != nil {
		return "", 0, err
	}
	dated(dir, final, q.At)

	if err := conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
		return "", 0, fmt.Errorf("acknowledging %s: %w", final, err)
	}
	return final, got, nil
}

// takeOver reads one item into a namespace and puts it where the caller said, over whatever is
// there. What was checked before the caller started sending is that the name still holds the
// version they believe it holds.
func takeOver(conn *wire.Conn, dir *os.Root, name string, q request, progress func(string, int64, int64)) (int64, error) {
	part, err := partName(name)
	if err != nil {
		return 0, err
	}

	got, err := land(conn, dir, arriving{part: part}, path.Base(name), q.Size, q.Mode, progress)
	if err != nil {
		return 0, err
	}

	if err := dir.Rename(part, name); err != nil {
		_ = dir.Remove(part)
		return 0, fmt.Errorf("renaming %s: %w", part, err)
	}
	dated(dir, name, q.At)

	if err := conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
		return 0, fmt.Errorf("acknowledging %s: %w", name, err)
	}
	return got, nil
}

// place moves a finished part onto a free name beside it, which is what it returns. Nothing that
// fails here leaves the part or the name it reached for lying about.
func place(dir *os.Root, part, name string) (string, error) {
	final, err := claim(dir, name)
	if err != nil {
		_ = dir.Remove(part)
		return "", fmt.Errorf("making room for %s: %w", name, err)
	}
	if err := dir.Rename(part, final); err != nil {
		_ = dir.Remove(final)
		_ = dir.Remove(part)
		return "", fmt.Errorf("renaming %s: %w", part, err)
	}
	return final, nil
}

// takeOnto reads one item and lands it on the path this side asked for. The part waits in the
// directory the item lands in, opened through it, so nothing on the way is followed.
func takeOnto(conn *wire.Conn, into, name string, e Entry, sum []byte, progress func(string, int64, int64)) error {
	where := filepath.Dir(into)
	dir, err := os.OpenRoot(where)
	if err != nil {
		return fmt.Errorf("opening %s: %w", where, err)
	}
	defer dir.Close()

	final := filepath.Base(into)
	at := arriving{}
	if len(sum) > 0 {
		at.part, at.have, at.kept = partFor(final, sum), already(where, final, sum), true
	} else if at.part, err = partName(final); err != nil {
		return err
	}

	if _, err := land(conn, dir, at, name, e.Size, e.Mode, progress); err != nil {
		return err
	}
	if err := dir.Rename(at.part, final); err != nil {
		_ = dir.Remove(at.part)
		return fmt.Errorf("renaming %s: %w", at.part, err)
	}
	dated(dir, final, e.At)

	return conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode())
}

// already is how much of an item of a known digest is already sitting beside where it lands.
func already(where, name string, sum []byte) int64 {
	stat, err := os.Lstat(filepath.Join(where, partFor(name, sum)))
	if err != nil || !stat.Mode().IsRegular() {
		return 0
	}
	return stat.Size()
}

// land reads one item into a part file and leaves it there, verified, trimmed and closed.
//
// What did not verify is thrown away and the sender is told why. What was cut off partway is kept
// when the part is named after the digest of what is coming, because that name is where the next
// attempt for the same bytes looks, and thrown away when it is not, because a name nothing can
// recognise is a name nothing will ever finish.
func land(conn *wire.Conn, dir *os.Root, a arriving, name string, size int64, mode uint32, progress func(string, int64, int64)) (int64, error) {
	out, seed, err := opening(dir, a)
	if err != nil {
		return 0, fmt.Errorf("opening %s: %w", a.part, err)
	}
	defer out.Close()

	lost := func(err error) (int64, error) {
		_ = dir.Remove(a.part)
		return 0, err
	}
	// Nothing half-arrived is left lying about under a name the next transfer would find.
	stopped := lost
	if a.kept {
		stopped = func(err error) (int64, error) { return 0, err }
	}

	got, reason, err := drain(conn, out, a, seed, name, size, progress)
	if err != nil {
		return stopped(err)
	}
	if reason != "" {
		// Thrown away before the sender is told, so that a sender who acts on being told finds it
		// already gone rather than racing the removal.
		_ = dir.Remove(a.part)
		_ = conn.WriteFrame(wire.KindAck, wire.Ack{Reason: reason}.Encode())
		return 0, fmt.Errorf("%s: %s", name, reason)
	}

	// The item is the whole of this file: what was hashed is what is kept, and the mode goes on the
	// open file rather than on a name somebody else could be holding by then.
	if err := out.Truncate(got); err != nil {
		return lost(fmt.Errorf("trimming %s: %w", a.part, err))
	}
	if err := out.Chmod(landing(mode)); err != nil {
		return lost(fmt.Errorf("setting the mode of %s: %w", a.part, err))
	}
	if err := out.Close(); err != nil {
		return lost(fmt.Errorf("closing %s: %w", a.part, err))
	}
	return got, nil
}

// drain reads a run of data frames into an open file and weighs what arrived against the count, the
// digest the sender ended with, and the size the round was opened on. A reason back means the item
// is not what was promised.
func drain(conn *wire.Conn, out *os.File, a arriving, digest *blake3.Hasher, name string, size int64, progress func(string, int64, int64)) (int64, string, error) {
	buf := make([]byte, wire.DataChunk)
	got := a.have

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
			if size != wire.SizeUnknown && got != size {
				return 0, fmt.Sprintf("arrived as %d bytes, and %d were announced", got, size), nil
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
			return 0, "", fmt.Errorf("writing %s: %w", a.part, err)
		}
		digest.Write(buf[:length])
		got += int64(length)
		if progress != nil {
			progress(name, got, size)
		}
	}
}

// partName is where an item waits while it arrives: beside where it lands, named after it and a tag
// drawn fresh for this transfer.
//
// The tag is what keeps two transfers of one name apart. Two peers pushing "report.bin" at once, or
// two gets landing on one path, each fill a file of their own, and neither can name the other's in
// advance. Nothing can carry one of these on either, which is the price of a name nobody else can
// guess.
func partName(name string) (string, error) {
	var tag [6]byte
	if _, err := rand.Read(tag[:]); err != nil {
		return "", fmt.Errorf("naming a part file for %s: %w", path.Base(name), err)
	}
	return path.Join(path.Dir(name), fmt.Sprintf(".%s.%x.part", path.Base(name), tag)), nil
}

// partFor is where an item of a known digest waits while it arrives.
//
// The same bytes give the same name every time, which is what lets a get that was killed halfway
// carry on instead of starting again. Named after the digest rather than after the name and the
// length: a name and a length are what a file has before and after somebody edits it, so resuming
// against those means carrying on top of bytes nobody wants any more.
func partFor(name string, sum []byte) string {
	if len(sum) > 16 {
		sum = sum[:16]
	}
	return path.Join(path.Dir(name), fmt.Sprintf(".%s.%x.part", path.Base(name), sum))
}

// opening makes or reopens the part file, and hands back the digest of what is already in it.
//
// A part named after this transfer alone is made with O_EXCL: whatever is at the name is neither
// unlinked nor written through, and what comes back was made here. A part named after the digest of
// what is coming is meant to be found again, so what is already in it is read back and weighed, and
// the item carries on from the end of it.
func opening(dir *os.Root, a arriving) (*os.File, *blake3.Hasher, error) {
	digest := blake3.New(32, nil)
	if a.have <= 0 {
		flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
		if a.kept {
			flags = os.O_CREATE | os.O_TRUNC | os.O_WRONLY
		}
		out, err := dir.OpenFile(a.part, flags, 0o600)
		return out, digest, err
	}

	held, err := dir.Open(a.part)
	if err != nil {
		return nil, nil, err
	}
	n, err := io.Copy(digest, io.LimitReader(held, a.have))
	held.Close()
	if err != nil {
		return nil, nil, err
	}
	if n != a.have {
		return nil, nil, fmt.Errorf("%s holds %d bytes of the %d it was carrying on from", a.part, n, a.have)
	}

	out, err := dir.OpenFile(a.part, os.O_WRONLY|os.O_APPEND, 0o600)
	return out, digest, err
}

// dated puts the modification time the item had where it came from on the item where it landed.
//
// Without it everything that arrives is dated now, so the next scan of a folder reads every
// arriving file as one somebody has just edited here and sends it straight back. A time nobody
// gave is left as it is.
func dated(dir *os.Root, name string, at int64) {
	if at <= 0 {
		return
	}
	when := time.Unix(0, at)
	_ = dir.Chtimes(name, when, when)
}

// landing is what a received file is allowed to be. The sender's bits are a stranger's opinion, so
// all that survives them is whether this is a program.
//
// The clamp is the reason mode is not one of the things two machines hold a file up against. What
// arrives as 0644 is kept as 0600 and what arrives as 0755 is kept as 0700, so comparing the mode
// here with the mode there would find them different every time, and each side would write the
// file back at the other for ever. Whether a file is a program is carried; the rest is not.
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
