package proto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"

	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// SecretBytes is the width of the derived secret.
const SecretBytes = 32

// nonceBytes is each side's contribution to the derivation.
const nonceBytes = 32

// pairMsg is what each side sends. The stream is already encrypted and both ends authenticated by
// libp2p, so the nonces need only be fresh, not secret in transit.
type pairMsg struct {
	From  string
	Proof []byte
	Addrs []string
	Name  string
	Nonce []byte
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
	return w.Body()
}

func decodePairMsg(body []byte) (pairMsg, error) {
	var out pairMsg

	r := wire.NewReader(body)
	from, err := r.String(wire.MaxString)
	if err != nil {
		return out, err
	}
	name, err := r.String(wire.MaxString)
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
func AnswerPairing(s Stream, self node.ID, name string, addrs []string) (Pairing, error) {
	var out Pairing

	conn := wire.NewConn(s)

	_, body, err := conn.ReadFrame()
	if err != nil {
		return out, err
	}
	theirs, err := decodePairMsg(body)
	if err != nil {
		return out, err
	}
	if len(theirs.Nonce) != nonceBytes {
		return out, fmt.Errorf("malformed pairing request")
	}

	mine := pairMsg{From: self.String(), Name: name, Addrs: addrs, Nonce: make([]byte, nonceBytes)}
	if _, err := rand.Read(mine.Nonce); err != nil {
		return out, err
	}
	if err := conn.WriteFrame(wire.KindOpen, mine.encode()); err != nil {
		return out, err
	}

	return finishPairing(self, theirs, mine)
}

// Pair runs the exchange from the initiating side.
func Pair(s Stream, self node.ID, name string, proof []byte, addrs []string) (Pairing, error) {
	var out Pairing

	conn := wire.NewConn(s)

	mine := pairMsg{From: self.String(), Name: name, Proof: proof, Addrs: addrs, Nonce: make([]byte, nonceBytes)}
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

	return finishPairing(self, theirs, mine)
}

// finishPairing is the half both sides share: the remote id comes from the connection, which the
// transport has already authenticated, so nothing here has to be taken on trust from the message.
func finishPairing(self node.ID, theirs, mine pairMsg) (Pairing, error) {
	var out Pairing

	remote, err := node.ParseID(theirs.From)
	if err != nil {
		return out, fmt.Errorf("reading the far end's id: %w", err)
	}

	secret, err := deriveSecret(self, remote, mine.Nonce, theirs.Nonce)
	if err != nil {
		return out, err
	}
	return Pairing{Peer: remote, Name: theirs.Name, Secret: secret, Proof: theirs.Proof, Addrs: theirs.Addrs}, nil
}
