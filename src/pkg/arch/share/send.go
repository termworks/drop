package share

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
	"lukechampine.com/blake3"

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
	stat   os.FileInfo
}

// Transfer is one logical batch, reusable only when delivery could not be confirmed.
type Transfer struct {
	mu      sync.Mutex
	id      transferID
	sources []Source
	replays []*replayBody
	ended   []wire.End
	done    []bool
	closed  bool
}

// NewTransfer prepares sources with one identity retained across retry attempts.
func NewTransfer(sources []Source) (*Transfer, error) {
	if err := validSources(sources); err != nil {
		return nil, err
	}
	var id transferID
	for !id.valid() {
		if _, err := rand.Read(id[:]); err != nil {
			return nil, fmt.Errorf("creating a transfer identity: %w", err)
		}
	}
	transfer := &Transfer{
		id:      id,
		sources: append([]Source(nil), sources...),
		replays: make([]*replayBody, len(sources)),
		ended:   make([]wire.End, len(sources)),
		done:    make([]bool, len(sources)),
	}
	for i, src := range transfer.sources {
		if src.Reader == nil {
			continue
		}
		replay, err := newReplay(src.Reader)
		if err != nil {
			_ = transfer.Close()
			return nil, fmt.Errorf("preparing %s for retry: %w", src.Name, err)
		}
		transfer.replays[i] = replay
	}
	return transfer, nil
}

// Known reports whether this source's length was settled before sending.
func (s Source) Known() bool { return s.Size >= 0 }

// FileFromPath describes a file on disk, whose size is known.
func FileFromPath(path string) (Source, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return Source{}, fmt.Errorf("cannot send %s: %w", path, err)
	}
	if !stat.Mode().IsRegular() {
		return Source{}, fmt.Errorf("cannot send %s: not a regular file", path)
	}
	return Source{
		Name: filepath.Base(path),
		Path: path,
		Size: stat.Size(),
		Mode: uint32(stat.Mode().Perm()),
		stat: stat,
	}, nil
}

// FileFromReader describes something whose length is not known until it ends.
func FileFromReader(name string, r io.Reader) Source {
	return Source{Name: name, Size: wire.SizeUnknown, Mode: 0o644, Reader: r}
}

// Send offers sources on an opened share namespace and writes the ones it accepts.
func Send(conn *wire.Conn, sources []Source, progress func(name string, done, total int64)) error {
	transfer, err := NewTransfer(sources)
	if err != nil {
		return err
	}
	defer func() { _ = transfer.Close() }()
	return transfer.Send(conn, progress)
}

// Send attempts this transfer over an opened share namespace.
func (t *Transfer) Send(conn *wire.Conn, progress func(name string, done, total int64)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return fmt.Errorf("transfer is closed")
	}
	return conn.WithIdle(wire.FiniteIdle, func() error {
		return t.send(conn, progress)
	})
}

// Close removes retry copies of streamed inputs.
func (t *Transfer) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	var out error
	for _, replay := range t.replays {
		if replay != nil {
			out = errors.Join(out, replay.close())
		}
	}
	return out
}

func validSources(sources []Source) error {
	if len(sources) > maxItems {
		return fmt.Errorf("offering %d items, over the %d limit", len(sources), maxItems)
	}
	for _, src := range sources {
		if src.Size < wire.SizeUnknown {
			return fmt.Errorf("%s has invalid size %d", src.Name, src.Size)
		}
	}
	return nil
}

func (t *Transfer) send(conn *wire.Conn, progress func(name string, done, total int64)) error {
	out := offer{ID: t.id, Items: make([]Item, 0, len(t.sources))}
	for _, src := range t.sources {
		out.Items = append(out.Items, Item{Name: src.Name, Size: src.Size, Mode: src.Mode})
	}
	if err := conn.WriteFrame(wire.KindItem, out.encode()); err != nil {
		return fmt.Errorf("offering: %w", err)
	}

	kind, body, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("reading the answer to the offer: %w", err)
	}
	switch kind {
	case wire.KindAccept:
	case wire.KindReject:
		reject, err := wire.DecodeReject(body)
		if err != nil {
			return err
		}
		return fmt.Errorf("the offer was refused: %s", reject.Reason)
	default:
		return fmt.Errorf("expected an answer to the offer, got frame kind %d", kind)
	}

	picked, err := decodeResume(body)
	if err != nil {
		return err
	}
	if len(picked.At) != len(t.sources) || len(picked.Done) != len(t.sources) {
		return fmt.Errorf("the answer covers %d items, expected %d", len(picked.At), len(t.sources))
	}
	for i, at := range picked.At {
		src := t.sources[i]
		if picked.Done[i] {
			if !t.done[i] || t.ended[i].Size != at {
				return fmt.Errorf("the answer completed unknown transfer state for %s", src.Name)
			}
			if src.Known() && at != src.Size {
				return fmt.Errorf("the answer completed %s at %d, expected %d", src.Name, at, src.Size)
			}
			continue
		}
		if !src.Known() && at != 0 && (t.replays[i] == nil || at > t.replays[i].cached) {
			return fmt.Errorf("the answer resumes unknown-size item %s at unavailable offset %d", src.Name, at)
		}
		if src.Known() && at > src.Size {
			return fmt.Errorf("the answer resumes %s at %d, beyond its %d bytes", src.Name, at, src.Size)
		}
	}

	for i, src := range t.sources {
		var err error
		if picked.Done[i] {
			err = sendCompleted(conn, src.Name, t.ended[i])
		} else {
			if t.replays[i] != nil {
				src.Reader = t.replays[i].reader()
			}
			err = sendOneTracked(conn, src, picked.At[i], progress, &t.ended[i], &t.done[i])
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func sendOne(conn *wire.Conn, src Source, at int64, progress func(string, int64, int64)) error {
	return sendOneTracked(conn, src, at, progress, nil, nil)
}

func sendOneTracked(conn *wire.Conn, src Source, at int64, progress func(string, int64, int64), ended *wire.End, complete *bool) error {
	body := src.Reader
	var opened *os.File
	var before os.FileInfo
	if body == nil {
		file, stat, err := openSource(src.Path)
		if err != nil {
			return fmt.Errorf("opening %s: %w", src.Path, err)
		}
		defer func() { _ = file.Close() }()
		if src.stat != nil && !sameSource(src.stat, stat) {
			return fmt.Errorf("opening %s: it changed before being sent", src.Path)
		}
		body, opened, before = file, file, stat
	}
	body = &progressReader{Reader: body}

	digest := blake3.New(32, nil)

	// A resumed prefix is hashed without being sent.
	if at > 0 {
		if _, err := io.CopyN(digest, body, at); err != nil {
			return fmt.Errorf("hashing the resumed part of %s: %w", src.Name, err)
		}
	}

	sent := at
	buf := make([]byte, wire.DataChunk)
	var localErr error

	for {
		n, err := body.Read(buf)
		if n > 0 {
			if src.Known() && int64(n) > src.Size-sent {
				localErr = fmt.Errorf("%s changed size while being sent: more than %d bytes", src.Name, src.Size)
				break
			}
			if werr := conn.WriteData(buf[:n]); werr != nil {
				return fmt.Errorf("sending %s: %w", src.Name, werr)
			}
			_, _ = digest.Write(buf[:n])
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

	if localErr == nil && src.Known() && sent != src.Size {
		localErr = fmt.Errorf("%s changed size while being sent: %d bytes, expected %d", src.Name, sent, src.Size)
	}
	if localErr == nil && opened != nil {
		after, err := opened.Stat()
		switch {
		case err != nil:
			localErr = fmt.Errorf("checking %s after it was sent: %w", src.Name, err)
		case !sameSource(before, after):
			localErr = fmt.Errorf("%s changed while being sent", src.Name)
		}
	}

	endDigest := digest.Sum(nil)
	if localErr != nil {
		endDigest = nil
	}
	end := wire.End{Size: sent, Digest: endDigest}
	if localErr == nil && ended != nil && complete != nil {
		*ended = wire.End{Size: end.Size, Digest: append([]byte(nil), end.Digest...)}
		*complete = true
	}
	if err := conn.WriteFrame(wire.KindEnd, end.Encode()); err != nil {
		if localErr != nil {
			return localErr
		}
		return unconfirmed(src.Name, err)
	}

	ack, err := confirmation(conn, src.Name)
	if localErr != nil {
		return localErr
	}
	if err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("%s was rejected: %s", src.Name, ack.Reason)
	}
	return nil
}

func sendCompleted(conn *wire.Conn, name string, end wire.End) error {
	if err := conn.WriteFrame(wire.KindEnd, end.Encode()); err != nil {
		return unconfirmed(name, err)
	}
	ack, err := confirmation(conn, name)
	if err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("%s was rejected: %s", name, ack.Reason)
	}
	return nil
}

func confirmation(conn *wire.Conn, name string) (wire.Ack, error) {
	kind, ackBody, err := conn.ReadFrame()
	if err != nil {
		return wire.Ack{}, unconfirmed(name, err)
	}
	if kind != wire.KindAck {
		return wire.Ack{}, fmt.Errorf("expected an ack for %s, got frame kind %d", name, kind)
	}
	ack, err := wire.DecodeAck(ackBody)
	if err != nil {
		return wire.Ack{}, err
	}
	return ack, nil
}

type unconfirmedError struct {
	name string
	err  error
}

func (e *unconfirmedError) Error() string {
	return fmt.Sprintf("waiting for %s to be confirmed: %v", e.name, e.err)
}

func (e *unconfirmedError) Unwrap() error { return e.err }

func unconfirmed(name string, err error) error {
	return &unconfirmedError{name: name, err: err}
}

// Unconfirmed reports whether a completed item may have landed without its verdict arriving.
func Unconfirmed(err error) bool {
	var target *unconfirmedError
	return errors.As(err, &target)
}

func openSource(path string) (*os.File, os.FileInfo, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !stat.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, fmt.Errorf("not a regular file")
	}
	return file, stat, nil
}

func sameSource(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) &&
		a.Mode().Perm() == b.Mode().Perm()
}

type progressReader struct {
	io.Reader
	emptyReads int
}

func (r *progressReader) Read(buf []byte) (int, error) {
	n, err := r.Reader.Read(buf)
	if n > 0 {
		r.emptyReads = 0
		return n, err
	}
	if err == nil {
		r.emptyReads++
		if r.emptyReads >= maxConsecutiveEmptyReads {
			return 0, io.ErrNoProgress
		}
	}
	return n, err
}

const maxConsecutiveEmptyReads = 100
