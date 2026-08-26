package files

import (
	"fmt"
	"io"
	"os"
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

// Get reads one file out of the namespace and writes it to a local path.
func (b *Browsing) Get(name, into string, progress func(name string, done, total int64)) error {
	got, err := b.round(request{Op: opGet, Name: name})
	if err != nil {
		return err
	}
	if !got.OK {
		return fmt.Errorf("reading %s: %s", shown(name), got.Reason)
	}

	size, mode := wire.SizeUnknown, uint32(0)
	if len(got.Entries) > 0 {
		size, mode = got.Entries[0].Size, got.Entries[0].Mode
	}
	return takeBody(b.conn, filepath.Base(name), into, size, mode, progress)
}

// Put writes one file into the namespace. It fails unless the far end said the namespace is
// writable.
func (b *Browsing) Put(name string, body io.Reader, size int64, mode uint32, progress func(name string, done, total int64)) error {
	got, err := b.round(request{Op: opPut, Name: name, Size: size, Mode: mode})
	if err != nil {
		return err
	}
	if !got.OK {
		return fmt.Errorf("writing %s: %s", shown(name), got.Reason)
	}
	return sendBody(b.conn, body, filepath.Base(name), size, progress)
}

// PutFile writes one file from this disk into the namespace.
func (b *Browsing) PutFile(name, from string, progress func(name string, done, total int64)) error {
	file, err := os.Open(from)
	if err != nil {
		return fmt.Errorf("opening %s: %w", from, err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("looking at %s: %w", from, err)
	}
	return b.Put(name, file, stat.Size(), uint32(stat.Mode().Perm()), progress)
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
