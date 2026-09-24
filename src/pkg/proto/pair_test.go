package proto

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// Every field a pairing message carries has to come back in the same order it went out.
//
// Encode and decode are two lists of calls that must stay in step; nothing in the type system
// keeps them there. Getting the nonce and the addresses the wrong way round produced a handshake
// that failed with a bare EOF on the far side, which says nothing about the cause.
func TestPairMsgRoundTrips(t *testing.T) {
	want := pairMsg{
		From:  "12D3KooWexample",
		Name:  "laptop",
		Proof: bytes.Repeat([]byte{0xab}, 32),
		Addrs: []string{"192.168.1.10:41234", "10.0.0.4:41234"},
		Nonce: bytes.Repeat([]byte{0xcd}, nonceBytes),
	}

	got, err := decodePairMsg(want.encode())
	if err != nil {
		t.Fatalf("decodePairMsg(): %v", err)
	}

	if got.From != want.From {
		t.Errorf("From = %q, want %q", got.From, want.From)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if !bytes.Equal(got.Proof, want.Proof) {
		t.Errorf("Proof = %x, want %x", got.Proof, want.Proof)
	}
	if !bytes.Equal(got.Nonce, want.Nonce) {
		t.Errorf("Nonce = %x, want %x", got.Nonce, want.Nonce)
	}
	if len(got.Addrs) != len(want.Addrs) {
		t.Fatalf("Addrs = %v, want %v", got.Addrs, want.Addrs)
	}
	for i := range want.Addrs {
		if got.Addrs[i] != want.Addrs[i] {
			t.Errorf("Addrs[%d] = %q, want %q", i, got.Addrs[i], want.Addrs[i])
		}
	}
}

// A message with no addresses and no proof is what the answering side sends.
func TestPairMsgRoundTripsWhenEmpty(t *testing.T) {
	want := pairMsg{From: "who", Name: "n", Nonce: bytes.Repeat([]byte{1}, nonceBytes)}

	got, err := decodePairMsg(want.encode())
	if err != nil {
		t.Fatalf("decodePairMsg(): %v", err)
	}
	if !bytes.Equal(got.Nonce, want.Nonce) {
		t.Fatalf("Nonce = %x, want %x", got.Nonce, want.Nonce)
	}
	if len(got.Addrs) != 0 {
		t.Fatalf("Addrs = %v, want none", got.Addrs)
	}
}

func TestPairMsgWritesOnlyWhatItsReaderAccepts(t *testing.T) {
	addrs := make([]string, maxPairAddrs+10)
	for i := range addrs {
		addrs[i] = "192.168.1.1:47777"
	}
	want := pairMsg{From: "who", Name: "n", Addrs: addrs, Nonce: bytes.Repeat([]byte{1}, nonceBytes)}

	got, err := decodePairMsg(want.encode())
	if err != nil {
		t.Fatalf("decodePairMsg(): %v", err)
	}
	if len(got.Addrs) != maxPairAddrs {
		t.Fatalf("pairing message has %d addresses, want %d", len(got.Addrs), maxPairAddrs)
	}
}

func TestPairingRefusesWrongFrameKinds(t *testing.T) {
	var framed bytes.Buffer
	message := pairMsg{From: "who", Name: "n", Nonce: bytes.Repeat([]byte{1}, nonceBytes)}
	if err := wire.NewConn(&framed).WriteFrame(wire.KindItem, message.encode()); err != nil {
		t.Fatal(err)
	}
	if _, err := readPairMsg(wire.NewConn(&framed)); err == nil {
		t.Fatal("readPairMsg() accepted a non-pairing frame")
	}
}

// Both sides must derive the same secret whichever direction they see the exchange from.
func TestDeriveSecretIsSymmetric(t *testing.T) {
	a, b := testEndpointID(t, 1), testEndpointID(t, 2)
	nonceA := bytes.Repeat([]byte{0xaa}, nonceBytes)
	nonceB := bytes.Repeat([]byte{0xbb}, nonceBytes)

	fromA, err := deriveSecret(a, b, nonceA, nonceB)
	if err != nil {
		t.Fatalf("deriveSecret() from a: %v", err)
	}
	fromB, err := deriveSecret(b, a, nonceB, nonceA)
	if err != nil {
		t.Fatalf("deriveSecret() from b: %v", err)
	}

	if !bytes.Equal(fromA, fromB) {
		t.Fatalf("the two sides derived different secrets:\n  %x\n  %x", fromA, fromB)
	}
	if len(fromA) != SecretBytes {
		t.Fatalf("secret is %d bytes, want %d", len(fromA), SecretBytes)
	}
}

// Whoever holds a pairing code may still only pair as themselves.
//
// The message says who the far end is and the transport proves it, and taking the first over the
// second would let anybody holding one code write a paired entry for a machine they have no key
// to -- which is a way into every path that admits paired devices.
func TestAPeerClaimingSomebodyElsesIdIsRefused(t *testing.T) {
	host, caller, victim := testEndpointID(t, 1), testEndpointID(t, 2), testEndpointID(t, 3)

	ours, theirs := net.Pipe()
	defer func() { _ = ours.Close() }()
	defer func() { _ = theirs.Close() }()

	go func() {
		conn := wire.NewConn(theirs)
		lie := pairMsg{From: victim.String(), Name: "laptop", Nonce: make([]byte, nonceBytes)}
		_ = conn.WriteFrame(wire.KindOpen, lie.encode())
		_, _, _ = conn.ReadFrame()
	}()

	if _, err := AnswerPairing(ours, host, caller, "host", nil, nil); err == nil {
		t.Fatal("a device paired under an id it does not hold")
	}
}

// The ordinary exchange: both sides come out with the id the transport proved for the other, and
// with the same secret.
func TestPairingKeepsTheIdTheTransportProved(t *testing.T) {
	a, b := testEndpointID(t, 1), testEndpointID(t, 2)

	one, two := net.Pipe()
	defer func() { _ = one.Close() }()
	defer func() { _ = two.Close() }()

	type answer struct {
		p   Pairing
		err error
	}
	answered := make(chan answer, 1)
	go func() {
		p, err := AnswerPairing(two, b, a, "host", nil, nil)
		answered <- answer{p, err}
	}()

	joined, err := Pair(one, a, b, "laptop", []byte("proof"), nil)
	if err != nil {
		t.Fatalf("Pair(): %v", err)
	}
	got := <-answered
	if got.err != nil {
		t.Fatalf("AnswerPairing(): %v", got.err)
	}

	if joined.Peer != b {
		t.Errorf("the joining side paired with %s, want %s", joined.Peer, b)
	}
	if got.p.Peer != a {
		t.Errorf("the offering side paired with %s, want %s", got.p.Peer, a)
	}
	if !bytes.Equal(joined.Secret, got.p.Secret) {
		t.Error("the two sides derived different secrets")
	}
	if got.p.Name != "laptop" {
		t.Errorf("the far end came out called %q", got.p.Name)
	}
}

// A name the far end chose becomes a key in the address book, so it has to look like a name.
func TestASuggestedNameIsBounded(t *testing.T) {
	for what, name := range map[string]string{
		"nothing":       "",
		"only spaces":   "   ",
		"a path":        "up/two",
		"another line":  "laptop\ndevice elsewhere",
		"an escape":     "laptop\x1b[2J",
		"a whole essay": strings.Repeat("x", mostName+1),
	} {
		if got := bookName(name); got != "" {
			t.Errorf("%s was taken as a name: %q", what, got)
		}
	}

	if got := bookName("  laptop  "); got != "laptop" {
		t.Errorf("an ordinary name came out as %q", got)
	}
}

// The bound holds on the wire as well, so an oversized name is a message that does not decode
// rather than one that is read and then thrown away.
func TestAnOversizedNameDoesNotDecode(t *testing.T) {
	sent := pairMsg{From: "who", Name: strings.Repeat("x", mostName+1), Nonce: make([]byte, nonceBytes)}

	if _, err := decodePairMsg(sent.encode()); err == nil {
		t.Fatal("a pairing message with an essay for a name decoded")
	}
}

// testEndpointID mints a distinct id without needing a network.
func testEndpointID(t *testing.T, seed byte) node.ID {
	t.Helper()

	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

// A pairing window answers whoever dials during it, so a dialler that opens a stream and says
// nothing must not hold a goroutine for the rest of the process's life.
func TestAPairingRequestThatSaysNothingIsNotHeldForever(t *testing.T) {
	host, caller := testEndpointID(t, 1), testEndpointID(t, 2)
	silent := &deadlined{set: make(chan struct{})}

	done := make(chan error, 1)
	go func() {
		_, err := AnswerPairing(silent, host, caller, "host", nil, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a pairing request that said nothing was answered")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AnswerPairing is still reading a stream that will never say anything")
	}
}

func TestAPairingResponseThatSaysNothingIsNotHeldForever(t *testing.T) {
	host, caller := testEndpointID(t, 1), testEndpointID(t, 2)
	silent := &deadlined{set: make(chan struct{})}

	done := make(chan error, 1)
	go func() {
		_, err := Pair(silent, caller, host, "caller", nil, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a pairing response that said nothing was accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Pair is still reading a stream that will never say anything")
	}
}

func TestAPairingAnswerThatCannotBeWrittenIsNotHeldForever(t *testing.T) {
	host, caller := testEndpointID(t, 1), testEndpointID(t, 2)
	message := pairMsg{From: caller.String(), Name: "caller", Nonce: make([]byte, nonceBytes)}
	var framed bytes.Buffer
	if err := wire.NewConn(&framed).WriteFrame(wire.KindOpen, message.encode()); err != nil {
		t.Fatal(err)
	}
	blocked := &writeDeadlined{read: &framed, set: make(chan struct{})}
	t.Cleanup(func() { _ = blocked.Close() })

	done := make(chan error, 1)
	go func() {
		_, err := AnswerPairing(blocked, host, caller, "host", nil, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a blocked pairing answer was written")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AnswerPairing is still writing to a peer that reads nothing")
	}
}

// A pairing the offering side refuses has to fail on the joining side as well. Answering first and
// deciding afterwards left the joiner with an address book entry for somebody who had thrown the
// attempt away, and every message it sent after that was refused as a stranger's.
func TestARefusedPairingFailsOnBothSides(t *testing.T) {
	a, b := testEndpointID(t, 1), testEndpointID(t, 2)

	one, two := net.Pipe()
	defer func() { _ = one.Close() }()
	defer func() { _ = two.Close() }()

	refused := make(chan error, 1)
	go func() {
		_, err := AnswerPairing(two, b, a, "host", nil, func(Pairing) error {
			return errors.New("that is not the code being shown")
		})
		refused <- err
	}()

	_, err := Pair(one, a, b, "laptop", []byte("wrong"), nil)
	if err == nil || !strings.Contains(err.Error(), "not the code being shown") {
		t.Fatalf("the joining side was not told it was refused: %v", err)
	}
	if err := <-refused; err == nil {
		t.Fatal("the offering side reported a refused pairing as a success")
	}
}

// What the offering side accepts is handed over before the far end hears it paired, so it can be
// written down first.
func TestAcceptSeesThePairingBeforeTheFarEndDoes(t *testing.T) {
	a, b := testEndpointID(t, 1), testEndpointID(t, 2)

	one, two := net.Pipe()
	defer func() { _ = one.Close() }()
	defer func() { _ = two.Close() }()

	var accepted atomic.Bool
	go func() {
		_, _ = AnswerPairing(two, b, a, "host", nil, func(p Pairing) error {
			if p.Peer != a {
				return fmt.Errorf("accepting a pairing with %s", p.Peer)
			}
			accepted.Store(true)
			return nil
		})
	}()

	if _, err := Pair(one, a, b, "laptop", []byte("proof"), nil); err != nil {
		t.Fatalf("Pair(): %v", err)
	}
	if !accepted.Load() {
		t.Fatal("the joining side finished before the offering side had accepted")
	}
}
