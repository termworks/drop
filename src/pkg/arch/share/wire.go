package share

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/wire"
)

// What a share says, once the namespace has been opened.
//
//	offer   the sender names every item and how big it is
//	resume  the receiver says how much of each it already holds
//	data…   the bytes of one item, then an end and an ack, then the next
//
// The offer is the first thing on the stream rather than part of opening it: what a share is
// pushing is a share's business, and nothing generic has to carry a list of file names to find out
// what kind of session this is.

// maxItems caps how many entries one offer may carry, so a small frame cannot ask for a huge slice.
const maxItems = 1 << 16

// Item describes one thing being sent. Name is a base name; a sender does not choose where on the
// receiving machine its bytes land.
type Item struct {
	Name string
	Size int64
	Mode uint32
}

// Known reports whether this item's length was settled before sending.
func (i Item) Known() bool { return i.Size >= 0 }

// offer is everything the sender has.
type offer struct {
	Items []Item
}

func (o offer) encode() []byte {
	w := wire.NewWriter()
	w.Uint(uint64(len(o.Items)))
	for _, item := range o.Items {
		w.String(item.Name)
		w.Int(item.Size)
		w.Uint(uint64(item.Mode))
	}
	return w.Body()
}

func decodeOffer(body []byte) (offer, error) {
	var out offer

	r := wire.NewReader(body)
	count, err := r.Uint()
	if err != nil {
		return out, err
	}
	if count > maxItems {
		return out, fmt.Errorf("an offer claims %d items, over the %d limit", count, maxItems)
	}

	out.Items = make([]Item, 0, wire.Hint(count, body, 3))
	for range count {
		name, err := r.String(wire.MaxString)
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
		out.Items = append(out.Items, Item{Name: name, Size: size, Mode: uint32(mode)})
	}
	return out, nil
}

// resume answers an offer: per item, how many bytes the receiver already holds. It is zero for an
// item whose size is unknown, because there is nothing to resume against.
type resume struct {
	At []int64
}

func (u resume) encode() []byte {
	w := wire.NewWriter()
	w.Uint(uint64(len(u.At)))
	for _, at := range u.At {
		w.Int(at)
	}
	return w.Body()
}

func decodeResume(body []byte) (resume, error) {
	var out resume

	r := wire.NewReader(body)
	count, err := r.Uint()
	if err != nil {
		return out, err
	}
	if count > maxItems {
		return out, fmt.Errorf("an answer claims %d items, over the %d limit", count, maxItems)
	}

	out.At = make([]int64, 0, wire.Hint(count, body, 1))
	for range count {
		at, err := r.Int()
		if err != nil {
			return out, err
		}
		out.At = append(out.At, at)
	}
	return out, nil
}
