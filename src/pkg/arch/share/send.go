package share

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	}, nil
}

// FileFromReader describes something whose length is not known until it ends.
func FileFromReader(name string, r io.Reader) Source {
	return Source{Name: name, Size: wire.SizeUnknown, Mode: 0o644, Reader: r}
}

// Send offers sources on an opened share namespace and writes the ones it accepts.
func Send(conn *wire.Conn, sources []Source, progress func(name string, done, total int64)) error {
	if len(sources) > maxItems {
		return fmt.Errorf("offering %d items, over the %d limit", len(sources), maxItems)
	}
	out := offer{}
	for _, src := range sources {
		if src.Size < wire.SizeUnknown {
			return fmt.Errorf("%s has invalid size %d", src.Name, src.Size)
		}
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
	if len(picked.At) != len(sources) {
		return fmt.Errorf("the answer covers %d items, expected %d", len(picked.At), len(sources))
	}
	for i, at := range picked.At {
		src := sources[i]
		if !src.Known() && at != 0 {
			return fmt.Errorf("the answer resumes unknown-size item %s at %d", src.Name, at)
		}
		if src.Known() && at > src.Size {
			return fmt.Errorf("the answer resumes %s at %d, beyond its %d bytes", src.Name, at, src.Size)
		}
	}

	for i, src := range sources {
		if err := sendOne(conn, src, picked.At[i], progress); err != nil {
			return err
		}
	}
	return nil
}

func sendOne(conn *wire.Conn, src Source, at int64, progress func(string, int64, int64)) error {
	body := src.Reader
	if body == nil {
		file, err := openSource(src.Path)
		if err != nil {
			return fmt.Errorf("opening %s: %w", src.Path, err)
		}
		defer func() { _ = file.Close() }()
		body = file
	}

	digest := blake3.New(32, nil)

	// The digest covers the whole item, so a resumed prefix is hashed without being sent. Only a
	// known-size item can be resumed; there is nothing to seek in a pipe.
	if at > 0 && src.Known() {
		if _, err := io.CopyN(digest, body, at); err != nil {
			return fmt.Errorf("hashing the resumed part of %s: %w", src.Name, err)
		}
	}

	sent := at
	buf := make([]byte, wire.DataChunk)

	for {
		n, err := body.Read(buf)
		if n > 0 {
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

	if src.Known() && sent != src.Size {
		return fmt.Errorf("%s changed size while being sent: %d bytes, expected %d", src.Name, sent, src.Size)
	}

	end := wire.End{Size: sent, Digest: digest.Sum(nil)}
	if err := conn.WriteFrame(wire.KindEnd, end.Encode()); err != nil {
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
	ack, err := wire.DecodeAck(ackBody)
	if err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("%s was rejected: %s", src.Name, ack.Reason)
	}
	return nil
}

func openSource(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("not a regular file")
	}
	return file, nil
}
