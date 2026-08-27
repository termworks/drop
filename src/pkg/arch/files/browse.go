package files

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/bresilla/drop/src/pkg/wire"
)

// Browsing is an open files namespace on another device.
type Browsing struct {
	conn     *wire.Conn
	writable bool
}

// Browse reads what the far end says about a namespace it has just accepted, and hands back the way
// to walk it.
func Browse(conn *wire.Conn) (*Browsing, error) {
	kind, body, err := conn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading what is there: %w", err)
	}
	switch kind {
	case wire.KindReply:
		y, err := decodeReady(body)
		if err != nil {
			return nil, err
		}
		return &Browsing{conn: conn, writable: y.Writable}, nil
	case wire.KindReject:
		reject, err := wire.DecodeReject(body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("that namespace cannot be walked: %s", reject.Reason)
	default:
		return nil, fmt.Errorf("expected what is there, got frame kind %d", kind)
	}
}

// Writable reports whether this namespace takes anything back.
func (b *Browsing) Writable() bool { return b.writable }

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

// Want is what a caller already knows about a file before it asks for it.
type Want struct {
	// Sum is the digest of the version wanted. A part file beside where the file lands is named
	// after it, so a get that was cut off carries on from what is already there rather than
	// starting again. Empty asks for whatever is at the name now, and cannot be carried on.
	Sum []byte
	// Progress, when set, is called as the bytes move.
	Progress func(name string, done, total int64)
}

// Given is what a caller says about a file it is writing.
type Given struct {
	Size int64
	Mode uint32
	// At is when it last changed here, in nanoseconds, so that what lands there is dated the way it
	// is dated here rather than dated now.
	At int64
	// Progress, when set, is called as the bytes move.
	Progress func(name string, done, total int64)
}

// Get reads one file out of the namespace and writes it to a local path, carrying on from whatever
// an earlier attempt at the same version left beside it.
func (b *Browsing) Get(name, into string, want Want) error {
	from := already(filepath.Dir(into), filepath.Base(into), want.Sum)

	got, err := b.round(request{Op: opGet, Name: name, Sum: want.Sum, From: from})
	if err != nil {
		return err
	}
	if !got.OK {
		return fmt.Errorf("reading %s: %s", shown(name), got.Reason)
	}

	e := Entry{Size: wire.SizeUnknown}
	if len(got.Entries) > 0 {
		e = got.Entries[0]
	}
	return takeOnto(b.conn, into, path.Base(name), e, want.Sum, want.Progress)
}

// Put writes one file into the namespace, on a free name beside whatever is already there. It fails
// unless the far end said the namespace is writable.
func (b *Browsing) Put(name string, body io.Reader, g Given) error {
	got, err := b.round(request{Op: opPut, Name: name, Size: g.Size, Mode: g.Mode, At: g.At})
	if err != nil {
		return err
	}
	if !got.OK {
		return fmt.Errorf("writing %s: %s", shown(name), got.Reason)
	}
	return sendBody(b.conn, body, path.Base(name), g.Size, 0, g.Progress)
}

// Replace writes one file over the version already at that name.
//
// was is the digest of the version the caller believes is there, and empty says the caller believes
// there is nothing there at all. The far end weighs it before a byte is sent, so a file somebody
// else changed in the meantime comes back as a refusal rather than being written over.
func (b *Browsing) Replace(name string, body io.Reader, was []byte, g Given) error {
	got, err := b.round(request{Op: opReplace, Name: name, Size: g.Size, Mode: g.Mode, At: g.At, Sum: was})
	if err != nil {
		return err
	}
	if !got.OK {
		return fmt.Errorf("replacing %s: %s", shown(name), got.Reason)
	}
	return sendBody(b.conn, body, path.Base(name), g.Size, 0, g.Progress)
}

// PutFile writes one file from this disk into the namespace.
func (b *Browsing) PutFile(name, from string, progress func(name string, done, total int64)) error {
	file, stat, err := lifted(from)
	if err != nil {
		return err
	}
	defer file.Close()

	return b.Put(name, file, given(stat, progress))
}

// ReplaceFile writes one file from this disk over the version already at a name.
func (b *Browsing) ReplaceFile(name, from string, was []byte, progress func(name string, done, total int64)) error {
	file, stat, err := lifted(from)
	if err != nil {
		return err
	}
	defer file.Close()

	return b.Replace(name, file, was, given(stat, progress))
}

// lifted opens a file on this disk and weighs it.
func lifted(from string) (*os.File, os.FileInfo, error) {
	file, err := os.Open(from)
	if err != nil {
		return nil, nil, fmt.Errorf("opening %s: %w", from, err)
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("looking at %s: %w", from, err)
	}
	return file, stat, nil
}

// given is what to say about a file on this disk that is about to be written elsewhere.
func given(stat os.FileInfo, progress func(name string, done, total int64)) Given {
	return Given{
		Size:     stat.Size(),
		Mode:     uint32(stat.Mode().Perm()),
		At:       stat.ModTime().UnixNano(),
		Progress: progress,
	}
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
