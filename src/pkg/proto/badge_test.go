package proto

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/wire"
	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/user"
)

func idFrom(seed byte) node.ID {
	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

// badgeFor signs a badge for one machine, the way enrolment does.
func badgeFor(t *testing.T, device node.ID, name string) (ssh.Signer, []byte, []byte) {
	t.Helper()

	_, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}

	badge, sig, err := user.Sign(signer, device.String(), name, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return signer, badge.Bytes(), sig
}

func TestABadgeSaysWhoseMachineIsCalling(t *testing.T) {
	device := idFrom(1)
	signer, badge, sig := badgeFor(t, device, "laptop")

	shown := vouched(device, Open{Badge: badge, Signed: sig})
	if !shown.Shown() {
		t.Fatal("a good badge was not believed")
	}
	if shown.Key != user.Text(signer.PublicKey()) {
		t.Error("the badge named a different user key")
	}
	if shown.As != "laptop" {
		t.Errorf("the machine is called %q", shown.As)
	}
}

// The badge names a device, and the transport proves one. A badge that does not name the machine
// presenting it is somebody replaying a badge that is not theirs.
func TestABadgeForAnotherMachineIsNotBelieved(t *testing.T) {
	_, badge, sig := badgeFor(t, idFrom(1), "laptop")

	if shown := vouched(idFrom(2), Open{Badge: badge, Signed: sig}); shown.Shown() {
		t.Fatal("a badge was believed on the wrong machine")
	}
}

func TestRubbishIsNotABadge(t *testing.T) {
	device := idFrom(1)
	_, badge, sig := badgeFor(t, device, "laptop")

	for what, open := range map[string]Open{
		"nothing at all":   {},
		"no signature":     {Badge: badge},
		"a bent signature": {Badge: badge, Signed: append([]byte("x"), sig...)},
		"a bent badge":     {Badge: append([]byte("x"), badge...), Signed: sig},
	} {
		if shown := vouched(device, open); shown.Shown() {
			t.Errorf("%s was taken for a badge", what)
		}
	}
}

// The hello ask carries the badge, because what a node is willing to say it serves depends on who
// is asking.
func TestABadgeRidesInTheHelloAsk(t *testing.T) {
	device := idFrom(1)
	signer, badge, sig := badgeFor(t, device, "laptop")

	Carry(badge, sig)
	defer Carry(nil, nil)

	shown := showing(device, showable())
	if shown.Key != user.Text(signer.PublicKey()) {
		t.Fatalf("the badge did not survive the ask: %+v", shown)
	}

	// An ask with no badge in it is not an ask this node knows how to answer for anybody.
	if shown := showing(device, nil); shown.Shown() {
		t.Error("an empty ask carried a badge")
	}
}

// Pairing carries the badge too.
func TestPairingCarriesABadge(t *testing.T) {
	_, badge, sig := badgeFor(t, idFrom(1), "laptop")

	sent := pairMsg{
		From:   idFrom(1).String(),
		Name:   "laptop",
		Proof:  []byte("proof"),
		Addrs:  []string{"10.0.0.1:1"},
		Nonce:  make([]byte, nonceBytes),
		Badge:  badge,
		Signed: sig,
	}

	got, err := decodePairMsg(sent.encode())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Badge) != string(badge) || string(got.Signed) != string(sig) {
		t.Error("the badge did not survive the exchange")
	}
	if got.Name != "laptop" || len(got.Addrs) != 1 {
		t.Error("the badge disturbed what was already there")
	}
}

// A frame in the shape a node without badges would send must not decode.
//
// Nothing has ever shipped that sends one, and nothing is going to: a short frame is a bug or
// somebody probing, and reading it as a caller with no person would be a way in that nobody
// declared. This is the assertion that the tolerance is gone and stays gone.
func TestAFrameWithNoBadgeInItIsRefused(t *testing.T) {
	short := wire.NewWriter()
	short.Byte(ModeMessages)
	short.String("laptop")
	short.String("/chat")
	short.String("")
	short.Uint(0)

	if _, err := decodeOpen(short.Body()); err == nil {
		t.Error("an open with no badge fields decoded")
	}

	// The same for pairing.
	stub := wire.NewWriter()
	stub.String(idFrom(1).String())
	stub.String("laptop")
	stub.Bytes([]byte("proof"))
	stub.Uint(0)
	stub.Bytes(make([]byte, nonceBytes))

	if _, err := decodePairMsg(stub.Body()); err == nil {
		t.Error("a pairing message with no badge fields decoded")
	}
}
