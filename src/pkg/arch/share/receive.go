package share

import (
	"bytes"
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

// safeName strips any directory part a sender put in a name, so an offer cannot write outside the
// receiving directory.
func safeName(name string) string {
	clean := filepath.Base(filepath.Clean("/" + name))
	if clean == "/" || clean == "." || clean == ".." {
		return ""
	}
	return clean
}

// partName is where an item waits while it arrives.
//
// Who is sending goes into the name along with the name and the length, so two peers offering a
// file of the same name and size never write into one file. Without that they would take turns
// writing into it and each be told at the end that theirs had arrived, when what is there is a
// weave of both and matches neither digest — or worse, matches one, and the other peer is told
// their file landed when somebody else's did.
//
// The sender is in it rather than the moment, so a peer whose connection dropped still comes back
// to its own half-written file and carries on from where it stopped.
func partName(from node.ID, item Item) string {
	name := safeName(item.Name)
	sum := blake3.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d", from, name, item.Size))
	return fmt.Sprintf(".%s.%x.part", name, sum[:6])
}

// offered reads an offer before anything is made for it. Two items on one name means the second
// landing on top of the first, which nobody asked for. A name that is not a file name is not one
// this will make a file for.
func offered(items []Item) error {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		name := safeName(item.Name)
		if name == "" {
			return fmt.Errorf("%q is not a file name", item.Name)
		}
		if seen[name] {
			return fmt.Errorf("%s was offered twice", name)
		}
		seen[name] = true
	}
	return nil
}

// receive reads the offer, answers it, and takes the items one at a time.
func receive(conn *wire.Conn, into string, from node.ID, hooks Into) error {
	kind, body, err := conn.ReadFrame()
	if err != nil {
		// A sender that closes before offering anything has pushed nothing, which is not a fault.
		if wire.Closed(err) {
			return nil
		}
		return fmt.Errorf("reading the offer from %s: %w", node.Brief(from), err)
	}
	if kind != wire.KindItem {
		return fmt.Errorf("%s sent frame kind %d, expected an offer", node.Brief(from), kind)
	}
	out, err := decodeOffer(body)
	if err != nil {
		return fmt.Errorf("reading the offer from %s: %w", node.Brief(from), err)
	}

	refuse := func(reason string) error {
		return conn.WriteFrame(wire.KindReject, wire.Reject{Reason: reason}.Encode())
	}

	if err := offered(out.Items); err != nil {
		_ = refuse(err.Error())
		return err
	}
	if err := os.MkdirAll(into, 0o700); err != nil {
		_ = refuse("cannot write here")
		return fmt.Errorf("creating %s: %w", into, err)
	}

	// Everything below happens through the open directory, which follows no symlink out of it: the
	// paths here are guessable, and the machine this runs on may have other people on it.
	dir, err := os.OpenRoot(into)
	if err != nil {
		_ = refuse("cannot write here")
		return fmt.Errorf("opening %s: %w", into, err)
	}
	defer dir.Close()

	picked := resume{At: make([]int64, len(out.Items))}
	for i, item := range out.Items {
		// Only a known-size item can be resumed: without a length there is no way to tell a partial
		// file from a complete one.
		if !item.Known() {
			continue
		}
		stat, err := dir.Lstat(partName(from, item))
		if err == nil && stat.Mode().IsRegular() && stat.Size() <= item.Size {
			picked.At[i] = stat.Size()
		}
	}
	if err := conn.WriteFrame(wire.KindAccept, picked.encode()); err != nil {
		return err
	}

	for i, item := range out.Items {
		if err := receiveOne(conn, dir, from, item, picked.At[i], hooks); err != nil {
			return err
		}
	}
	return nil
}

// opening makes the part file this item is written into, and says where in it to carry on.
//
// A root refuses a link that leaves it, and follows one that does not — so a name inside the
// receiving directory can still be aimed at another file in it. Starting fresh therefore unlinks
// first and insists on making the file itself; picking something up opens what is there without
// making anything, and carries on only if what opened is a plain file long enough to hold what was
// promised. Anything else starts again rather than writing somewhere nobody chose.
func opening(dir *os.Root, part string, at int64) (*os.File, int64, error) {
	if at > 0 {
		// Lstat looks at the name rather than through it, so a link is seen for what it is. What
		// opened is then checked against what was looked at, because between the two somebody could
		// have put something else there.
		if named, err := dir.Lstat(part); err == nil && named.Mode().IsRegular() && named.Size() >= at {
			out, err := dir.OpenFile(part, os.O_RDWR, 0o600)
			if err == nil {
				if opened, serr := out.Stat(); serr == nil && os.SameFile(named, opened) {
					return out, at, nil
				}
				out.Close()
			}
		}
	}

	_ = dir.Remove(part)
	out, err := dir.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("opening %s: %w", part, err)
	}
	return out, 0, nil
}

func receiveOne(conn *wire.Conn, dir *os.Root, from node.ID, item Item, at int64, hooks Into) error {
	name := safeName(item.Name)
	part := partName(from, item)

	out, at, err := opening(dir, part, at)
	if err != nil {
		return err
	}
	defer out.Close()

	digest := blake3.New(32, nil)
	if at > 0 {
		if _, err := io.CopyN(digest, io.NewSectionReader(out, 0, at), at); err != nil {
			return fmt.Errorf("rehashing %s: %w", part, err)
		}
	}

	if _, err := out.Seek(at, io.SeekStart); err != nil {
		return fmt.Errorf("seeking in %s: %w", part, err)
	}

	// Data frames run until the item ends, which is what lets an item arrive whose length nobody
	// knew when it started.
	got := at
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
			end, err := wire.DecodeEnd(endBody)
			if err != nil {
				return err
			}
			return finishOne(conn, dir, from, item, name, part, out, digest, got, end, hooks)
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
		if hooks.Progress != nil {
			hooks.Progress(name, got, item.Size)
		}
	}
}

func finishOne(conn *wire.Conn, dir *os.Root, from node.ID, item Item, name, part string, out *os.File, digest *blake3.Hasher, got int64, end wire.End, hooks Into) error {
	refuse := func(reason string) error {
		_ = dir.Remove(part)
		_ = conn.WriteFrame(wire.KindAck, wire.Ack{Reason: reason}.Encode())
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

	if err := conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
		return fmt.Errorf("acknowledging %s: %w", final, err)
	}
	if hooks.Landed != nil {
		hooks.Landed(from, final, got)
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
