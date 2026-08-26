package proto

import (
	"bytes"
	"net"
	"strings"
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
	defer ours.Close()
	defer theirs.Close()

	go func() {
		conn := wire.NewConn(theirs)
		lie := pairMsg{From: victim.String(), Name: "laptop", Nonce: make([]byte, nonceBytes)}
		_ = conn.WriteFrame(wire.KindOpen, lie.encode())
		_, _, _ = conn.ReadFrame()
	}()

	if _, err := AnswerPairing(ours, host, caller, "host", nil); err == nil {
		t.Fatal("a device paired under an id it does not hold")
	}
}

// The ordinary exchange: both sides come out with the id the transport proved for the other, and
// with the same secret.
func TestPairingKeepsTheIdTheTransportProved(t *testing.T) {
	a, b := testEndpointID(t, 1), testEndpointID(t, 2)

	one, two := net.Pipe()
	defer one.Close()
	defer two.Close()

	type answer struct {
		p   Pairing
		err error
	}
	answered := make(chan answer, 1)
	go func() {
		p, err := AnswerPairing(two, b, a, "host", nil)
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
		_, err := AnswerPairing(silent, host, caller, "host", nil)
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
