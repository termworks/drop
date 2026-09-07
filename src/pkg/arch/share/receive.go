package share

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/keep"
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
// The sender and transfer identity go into the name with the item metadata. A retry finds its own
// partial file, while separate transfers of the same name and size use separate files.
func partName(from node.ID, transfer transferID, item Item) string {
	name := safeName(item.Name)
	sum := blake3.Sum256(fmt.Appendf(nil, "%s\x00%x\x00%s\x00%d", from, transfer, name, item.Size))
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
		if item.Size < wire.SizeUnknown {
			return fmt.Errorf("%s has invalid size %d", name, item.Size)
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
	quota := quotaFor(Config{})
	return receiveWithReceipts(conn, into, from, hooks, nil, &quota)
}

func receiveWithReceipts(conn *wire.Conn, into string, from node.ID, hooks Into, receipts *configInstance, quota *transferQuota) error {
	return conn.WithIdle(wire.FiniteIdle, func() error {
		return receiveWithin(conn, into, from, hooks, receipts, quota)
	})
}

func receiveWithin(conn *wire.Conn, into string, from node.ID, hooks Into, receipts *configInstance, quota *transferQuota) error {
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
	defer func() { _ = dir.Close() }()
	locked, err := lockLanding(dir)
	if err != nil {
		reason := "cannot write here"
		if errors.Is(err, errLandingBusy) {
			reason = errLandingBusy.Error()
		}
		_ = refuse(reason)
		return fmt.Errorf("locking %s: %w", into, err)
	}
	defer func() { _ = locked.Close() }()

	picked := resume{At: make([]int64, len(out.Items)), Done: make([]bool, len(out.Items))}
	completed := make([]receipt, len(out.Items))
	for i, item := range out.Items {
		key := receiptKey{from: from, transfer: out.ID, item: uint32(i)}
		stored, found, err := receipts.lookup(key, item)
		if err != nil {
			_ = refuse(err.Error())
			return err
		}
		if found {
			picked.At[i], picked.Done[i], completed[i] = stored.size, true, stored
			continue
		}
		stat, err := dir.Lstat(partName(from, out.ID, item))
		if err == nil && stat.Mode().IsRegular() && (!item.Known() || stat.Size() <= item.Size) {
			picked.At[i] = stat.Size()
		}
	}
	if reason := quota.preflight(out.Items, picked); reason != "" {
		_ = refuse(reason)
		return fmt.Errorf("receiving from %s: %s", node.Brief(from), reason)
	}
	want := int64(0)
	for i, item := range out.Items {
		if picked.Done[i] || !item.Known() {
			continue
		}
		left := item.Size - picked.At[i]
		if left > math.MaxInt64-want {
			want = math.MaxInt64
			break
		}
		want += left
	}
	if err := keep.RoomIn(dir, want); err != nil {
		_ = refuse("not enough free space")
		return fmt.Errorf("receiving from %s: %w", node.Brief(from), err)
	}
	if err := conn.WriteFrame(wire.KindAccept, picked.encode()); err != nil {
		return err
	}

	for i, item := range out.Items {
		key := receiptKey{from: from, transfer: out.ID, item: uint32(i)}
		var err error
		if picked.Done[i] {
			err = receiveCompleted(conn, completed[i], key, receipts, from, hooks)
		} else {
			err = receiveOne(conn, dir, from, out.ID, uint32(i), item, picked.At[i], hooks, receipts, quota)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

var errLandingBusy = errors.New("another transfer is already landing here")

// lockLanding owns the receiving directory until the session ends.
func lockLanding(dir *os.Root) (*os.File, error) {
	locked, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	stat, err := locked.Stat()
	if err != nil || !stat.IsDir() {
		_ = locked.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("receiving root is not a directory")
	}
	for {
		err = unix.Flock(int(locked.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		_ = locked.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %v", errLandingBusy, err)
		}
		return nil, err
	}
	return locked, nil
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
				_ = out.Close()
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

func receiveOne(conn *wire.Conn, dir *os.Root, from node.ID, transfer transferID, index uint32, item Item, at int64, hooks Into, receipts *configInstance, quota *transferQuota) error {
	name := safeName(item.Name)
	part := partName(from, transfer, item)

	out, at, err := opening(dir, part, at)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

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
			key := receiptKey{from: from, transfer: transfer, item: index}
			return finishOne(conn, dir, from, item, key, name, part, out, digest, got, end, hooks, receipts)
		}
		if kind != wire.KindData {
			return fmt.Errorf("expected data for %s, got frame kind %d", name, kind)
		}
		if size == 0 {
			return fmt.Errorf("empty data frame for %s", name)
		}

		if err := conn.ReadBody(buf, size); err != nil {
			return err
		}
		if item.Known() && int64(size) > item.Size-got {
			_ = dir.Remove(part)
			return fmt.Errorf("%s sent more than the announced %d bytes", name, item.Size)
		}
		if err := quota.take(got, int64(size)); err != nil {
			_ = dir.Remove(part)
			_ = conn.WriteFrame(wire.KindAck, wire.Ack{Reason: err.Error()}.Encode())
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := keep.Room(out, int64(size)); err != nil {
			_ = dir.Remove(part)
			return fmt.Errorf("%s: not enough free space: %w", name, err)
		}
		if _, err := out.Write(buf[:size]); err != nil {
			return fmt.Errorf("writing %s: %w", part, err)
		}
		_, _ = digest.Write(buf[:size])
		got += int64(size)
		if hooks.Progress != nil {
			hooks.Progress(name, got, item.Size)
		}
	}
}

func finishOne(conn *wire.Conn, dir *os.Root, from node.ID, item Item, key receiptKey, name, part string, out *os.File, digest *blake3.Hasher, got int64, end wire.End, hooks Into, receipts *configInstance) error {
	refuse := func(reason string) error {
		_ = dir.Remove(part)
		_ = conn.WriteFrame(wire.KindAck, wire.Ack{Reason: reason}.Encode())
		return fmt.Errorf("%s: %s", name, reason)
	}

	if got != end.Size {
		return refuse(fmt.Sprintf("arrived as %d bytes, sender counted %d", got, end.Size))
	}
	if item.Known() && got != item.Size {
		return refuse(fmt.Sprintf("arrived as %d bytes, and %d were announced", got, item.Size))
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
	if err := out.Sync(); err != nil {
		return fmt.Errorf("syncing %s: %w", part, err)
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
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("syncing the receiving directory: %w", err)
	}
	var sum [32]byte
	copy(sum[:], end.Digest)
	receipts.remember(key, item, final, got, sum)

	if err := conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
		return fmt.Errorf("acknowledging %s: %w", final, err)
	}
	if receipts.report(key) && hooks.Landed != nil {
		hooks.Landed(from, final, got)
	}
	return nil
}

func receiveCompleted(conn *wire.Conn, stored receipt, key receiptKey, receipts *configInstance, from node.ID, hooks Into) error {
	kind, body, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("confirming completed %s: %w", stored.name, err)
	}
	if kind != wire.KindEnd {
		return fmt.Errorf("expected an end for completed %s, got frame kind %d", stored.name, kind)
	}
	end, err := wire.DecodeEnd(body)
	if err != nil {
		return err
	}
	if end.Size != stored.size || !bytes.Equal(end.Digest, stored.digest[:]) {
		reason := "completed item does not match its receipt"
		_ = conn.WriteFrame(wire.KindAck, wire.Ack{Reason: reason}.Encode())
		return fmt.Errorf("%s: %s", stored.name, reason)
	}
	if err := conn.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
		return fmt.Errorf("acknowledging %s: %w", stored.name, err)
	}
	if receipts.report(key) && hooks.Landed != nil {
		hooks.Landed(from, stored.name, stored.size)
	}
	return nil
}

func syncDir(dir *os.Root) error {
	opened, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = opened.Close() }()
	return opened.Sync()
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
			if err := f.Close(); err != nil {
				_ = dir.Remove(at)
				return "", err
			}
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
