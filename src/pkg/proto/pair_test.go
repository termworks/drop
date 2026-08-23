package proto

import (
	"bytes"
	"testing"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
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

// testEndpointID mints a distinct id without needing a network.
func testEndpointID(t *testing.T, seed byte) node.ID {
	t.Helper()

	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}
