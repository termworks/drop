package wire

// The bodies that belong to a frame kind rather than to a conversation.
//
// An end, an ack and a refusal say the same thing wherever they appear: this item is over and here
// is what it weighed and hashed to, here is the verdict on it, and here is why not. They live with
// the kinds they name, because everything that moves bytes over a framed stream writes them.

// SizeUnknown is the size of an item whose length is not known before it is sent: a pipe, a
// growing file, a terminal. The receiver reads until the item ends rather than counting down.
const SizeUnknown int64 = -1

// End closes one item: how much was actually sent, and the digest of all of it. For an
// unknown-size item this is where the length is finally learned.
type End struct {
	Size   int64
	Digest []byte
}

func (e End) Encode() []byte {
	w := NewWriter()
	w.Int(e.Size)
	w.Bytes(e.Digest)
	return w.Body()
}

func DecodeEnd(body []byte) (End, error) {
	var out End

	r := NewReader(body)
	size, err := r.Int()
	if err != nil {
		return out, err
	}
	digest, err := r.Bytes(64)
	if err != nil {
		return out, err
	}
	out.Size = size
	out.Digest = append([]byte(nil), digest...)
	return out, nil
}

// Ack is the receiver's verdict on one item, after hashing what arrived.
type Ack struct {
	OK     bool
	Reason string
}

func (a Ack) Encode() []byte {
	w := NewWriter()
	w.Bool(a.OK)
	w.String(a.Reason)
	return w.Body()
}

func DecodeAck(body []byte) (Ack, error) {
	var out Ack

	r := NewReader(body)
	ok, err := r.Bool()
	if err != nil {
		return out, err
	}
	reason, err := r.String(MaxString)
	if err != nil {
		return out, err
	}
	out.OK, out.Reason = ok, reason
	return out, nil
}

// Reject declines what was just asked for, and says why.
type Reject struct {
	Reason string
}

func (x Reject) Encode() []byte {
	w := NewWriter()
	w.String(x.Reason)
	return w.Body()
}

func DecodeReject(body []byte) (Reject, error) {
	r := NewReader(body)
	reason, err := r.String(MaxString)
	return Reject{Reason: reason}, err
}

// Hint bounds a pre-allocation by what a body could possibly hold: least is the fewest bytes one
// element can encode in. A count is a claim, and a seven-byte frame must not commit megabytes.
func Hint(count uint64, body []byte, least int) int {
	most := uint64(len(body) / least)
	if count > most {
		count = most
	}
	return int(count)
}
