package proto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"

	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// SecretBytes is the width of the derived secret.
const SecretBytes = 32

// nonceBytes is each side's contribution to the derivation.
const nonceBytes = 32

// mostName is as long a name as a far end may suggest for itself. It becomes a key in the address
// book and something a person types, not somewhere to put a paragraph.
const mostName = 64

// pairMsg is what each side sends. The stream is already encrypted and both ends authenticated by
// libp2p, so the nonces need only be fresh, not secret in transit.
type pairMsg struct {
	From  string
	Proof []byte
	Addrs []string
	Name  string
	Nonce []byte
	// Badge and Signed say who this machine belongs to, so that pairing is with a person rather
	// than only with the device in front of you.
	Badge  []byte
	Signed []byte
}

func (m pairMsg) encode() []byte {
	w := wire.NewWriter()
	w.String(m.From)
	w.String(m.Name)
	w.Bytes(m.Proof)
	w.Uint(uint64(len(m.Addrs)))
	for _, a := range m.Addrs {
		w.String(a)
	}
	w.Bytes(m.Nonce)
	w.Bytes(m.Badge)
	w.Bytes(m.Signed)
	return w.Body()
}

func decodePairMsg(body []byte) (pairMsg, error) {
	var out pairMsg

	r := wire.NewReader(body)
	from, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	name, err := r.String(mostName)
	if err != nil {
		return out, err
	}
	proof, err := r.Bytes(64)
	if err != nil {
		return out, err
	}
	out.From = from
	out.Name = name
	out.Proof = append([]byte(nil), proof...)

	count, err := r.Uint()
	if err != nil {
		return out, err
	}
	if count > 32 {
		return out, fmt.Errorf("a pairing message claims %d addresses", count)
	}
	for i := uint64(0); i < count; i++ {
		a, err := r.String(64)
		if err != nil {
			return out, err
		}
		out.Addrs = append(out.Addrs, a)
	}
	nonce, err := r.Bytes(nonceBytes)
	if err != nil {
		return out, err
	}
	out.Nonce = append([]byte(nil), nonce...)

	badge, err := r.Bytes(wire.MaxString)
	if err != nil {
		return out, err
	}
	signed, err := r.Bytes(wire.MaxString)
	if err != nil {
		return out, err
	}
	out.Badge, out.Signed = badge, signed
	return out, nil
}

// Pairing is the outcome of a successful exchange.
type Pairing struct {
	Peer   node.ID
	Name   string
	Secret []byte
	// Proof is what the initiator sent to show it held the pairing code.
	Proof []byte
	Addrs []string
	// User is the far end's user key, if its badge checked out. Pairing with a person means
	// keeping this: it is what lets their other machines be recognised without pairing again.
	User string
	// Machine is what they call this machine of theirs.
	Machine string
}

// NewCode generates a one-time pairing code: 60 bits, which is far past guessing when the only way
// to test a guess is a DHT lookup.
func NewCode() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating a pairing code: %w", err)
	}

	text := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))[:12]
	return fmt.Sprintf("%s-%s-%s", text[0:4], text[4:8], text[8:12]), nil
}

// deriveSecret mixes both nonces under both peer ids. The inputs are ordered by peer id so the two
// sides, which see the exchange from opposite directions, compute the same secret.
func deriveSecret(self, other node.ID, selfNonce, otherNonce []byte) ([]byte, error) {
	lo, hi := self, other
	loNonce, hiNonce := selfNonce, otherNonce
	if self.Compare(other) > 0 {
		lo, hi = other, self
		loNonce, hiNonce = otherNonce, selfNonce
	}

	ikm := append(append([]byte{}, loNonce...), hiNonce...)
	salt := append([]byte(lo.String()), []byte(hi.String())...)

	secret := make([]byte, SecretBytes)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("drop pair v1")), secret); err != nil {
		return nil, fmt.Errorf("deriving the shared secret: %w", err)
	}
	return secret, nil
}

// AnswerPairing completes the exchange from the receiving side, returning what was derived.
//
// from is the id the transport authenticated for the far end. It is the id that is paired with;
// what the message says about itself is only checked against it.
func AnswerPairing(s Stream, self, from node.ID, name string, addrs []string) (Pairing, error) {
	var out Pairing

	conn := wire.NewConn(s)

	// A pairing window is open to whoever dials during it, so the request is bounded: a stream that
	// says nothing is a goroutine held for the rest of the process's life.
	_ = s.SetReadDeadline(time.Now().Add(settleIn))

	_, body, err := conn.ReadFrame()
	if err != nil {
		return out, err
	}
	_ = s.SetReadDeadline(time.Time{})

	theirs, err := decodePairMsg(body)
	if err != nil {
		return out, err
	}
	if len(theirs.Nonce) != nonceBytes {
		return out, fmt.Errorf("malformed pairing request")
	}

	mine := pairMsg{From: self.String(), Name: name, Addrs: addrs, Nonce: make([]byte, nonceBytes)}
	mine.Badge, mine.Signed = carried()
	if _, err := rand.Read(mine.Nonce); err != nil {
		return out, err
	}
	if err := conn.WriteFrame(wire.KindOpen, mine.encode()); err != nil {
		return out, err
	}

	return finishPairing(self, from, theirs, mine)
}

// Pair runs the exchange from the initiating side. from is the id the transport authenticated for
// the device whose ticket is being answered.
func Pair(s Stream, self, from node.ID, name string, proof []byte, addrs []string) (Pairing, error) {
	var out Pairing

	conn := wire.NewConn(s)

	mine := pairMsg{From: self.String(), Name: name, Proof: proof, Addrs: addrs, Nonce: make([]byte, nonceBytes)}
	mine.Badge, mine.Signed = carried()
	if _, err := rand.Read(mine.Nonce); err != nil {
		return out, fmt.Errorf("generating a nonce: %w", err)
	}
	if err := conn.WriteFrame(wire.KindOpen, mine.encode()); err != nil {
		return out, fmt.Errorf("sending the pairing request: %w", err)
	}

	_, body, err := conn.ReadFrame()
	if err != nil {
		return out, fmt.Errorf("reading the pairing response: %w", err)
	}
	theirs, err := decodePairMsg(body)
	if err != nil {
		return out, fmt.Errorf("reading the pairing response: %w", err)
	}
	if len(theirs.Nonce) != nonceBytes {
		return out, fmt.Errorf("malformed pairing response")
	}

	return finishPairing(self, from, theirs, mine)
}

// finishPairing is the half both sides share: the remote id is the one the connection proved, so
// nothing here is taken on trust from the message.
//
// The id in the message is a cross-check and nothing more. A device that names somebody else's id
// is trying to write an address book entry for a machine it does not hold the key to, and the
// exchange ends there rather than filing it.
func finishPairing(self, from node.ID, theirs, mine pairMsg) (Pairing, error) {
	var out Pairing

	claimed, err := node.ParseID(theirs.From)
	if err != nil {
		return out, fmt.Errorf("reading the far end's id: %w", err)
	}
	if claimed != from {
		return out, fmt.Errorf("pairing with %s: it calls itself %s", from, claimed)
	}

	secret, err := deriveSecret(self, from, mine.Nonce, theirs.Nonce)
	if err != nil {
		return out, err
	}
	out = Pairing{Peer: from, Name: bookName(theirs.Name), Secret: secret, Proof: theirs.Proof, Addrs: theirs.Addrs}

	// The badge is checked against the id the transport proved, so a message claiming somebody
	// else's badge is worth exactly nothing.
	if badge := vouched(from, Opening{Badge: theirs.Badge, Signed: theirs.Signed}); badge.Shown() {
		out.User, out.Machine = badge.Key, badge.As
	}
	return out, nil
}

// bookName is what a far end's suggestion of what to call it is worth as an address book key.
//
// A name the far end chose is a convenience, and it ends up as a key in a file and as something
// typed on a command line. Anything with a control character, a newline or a path separator in it
// is worth nothing, and so is anything longer than a name; the caller falls back to the id.
func bookName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > mostName {
		return ""
	}

	for _, r := range name {
		if r < ' ' || r == 0x7f || r == '/' || r == '\\' {
			return ""
		}
	}
	return name
}
