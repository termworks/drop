package files

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/wire"
)

// Operations a files session can ask for.
//
// A put and a replace are different acts. A put is somebody handing a file over, and it lands on a
// free name so that nothing already here is lost to it. A replace is one version of a file taking
// the place of another, and it names the version it believes is there, so two machines writing at
// once is noticed rather than one of them winning silently.
const (
	opList    byte = 1
	opGet     byte = 2
	opPut     byte = 3
	opRemove  byte = 4
	opMkdir   byte = 5
	opMove    byte = 6
	opReplace byte = 7
)

// MaxRel bounds a path named inside a namespace. It arrives from a peer and becomes a filesystem
// path, so it is measured before it is looked at.
const MaxRel = 1024

// MaxEntries caps one listing, so a directory cannot be answered with an unbounded slice.
const MaxEntries = 1 << 14

// MaxSum bounds a digest named in a request, which is a blake3 sum and nothing longer.
const MaxSum = 32

// Entry is one thing in a served directory.
type Entry struct {
	Name string
	Size int64
	Mode uint32
	Dir  bool
	// At is when it last changed, in nanoseconds.
	//
	// Nanoseconds because that is what the filesystem holds and what two saves a few milliseconds
	// apart are told apart by. At whole seconds they are one save, and a folder kept level by
	// comparing times would never see the second one.
	At int64
}

// ready is the first thing the far end says: what it will let the caller do here.
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

// request is one operation. Size, Mode and At describe an upload; To is the destination of a move.
// The rest of the time they are zero.
type request struct {
	Op   byte
	Name string
	To   string
	Size int64
	Mode uint32
	// At is when the item last changed where it came from, in nanoseconds.
	At int64
	// Sum is the digest the round turns on: what a replace believes is at the name now, and what a
	// get is carrying on towards. Empty is a caller that does not know or does not care.
	Sum []byte
	// From is how much of the item the caller already holds, so a get that was interrupted carries
	// on rather than starting again.
	From int64
}

func (q request) encode() []byte {
	w := wire.NewWriter()
	w.Byte(q.Op)
	w.String(q.Name)
	w.String(q.To)
	w.Int(q.Size)
	w.Uint(uint64(q.Mode))
	w.Int(q.At)
	w.Bytes(q.Sum)
	w.Int(q.From)
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
	at, err := r.Int()
	if err != nil {
		return out, err
	}
	sum, err := r.Bytes(MaxSum)
	if err != nil {
		return out, err
	}
	from, err := r.Int()
	if err != nil {
		return out, err
	}
	out.Op, out.Name, out.To, out.Size, out.Mode = op, name, to, size, uint32(mode)
	out.At, out.Sum, out.From = at, append([]byte(nil), sum...), from
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
	out.Entries = make([]Entry, 0, wire.Hint(count, body, 5))
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
