package plate

import (
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/plain"
	"github.com/tmc/go-iroh/key"
)

// What a machine says about itself and about what it became, as bytes a stranger chose.
//
// Both are read before anybody has decided anything about the caller, and both end in a signature
// check — so what matters is that nothing before the check can panic, and that nothing which fails
// the check can come back looking like it passed.

func FuzzReadStamp(f *testing.F) {
	f.Add([]byte("drop-plate/1\nmachine a\nendpoint b\nwhose me\nuntil 2026-01-01T00:00:00Z\n"), make([]byte, 64))
	f.Add([]byte("drop-plate/1\n"), []byte{})
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, signed, sig []byte) {
		stamp, err := Read(signed, sig, time.Unix(0, 0))
		if err != nil {
			return
		}

		// Nothing gets past Read without being a real stamp: written the one way a stamp is
		// written, in date, and naming something that can be shown.
		if string(stamp.Bytes()) != string(signed) {
			t.Fatalf("a stamp came back that is not what was checked:\n got %q\nwant %q", stamp.Bytes(), signed)
		}
		if stamp.Machine.IsZero() || stamp.Endpoint.IsZero() {
			t.Fatal("a stamp came back naming nothing")
		}
		if !plain.Fit(stamp.Whose, MaxName) {
			t.Fatalf("a stamp came back naming %q, which cannot be shown", stamp.Whose)
		}
		if stamp.Expired(time.Unix(0, 0)) {
			t.Fatal("a stamp came back that had already run out")
		}
	})
}

func FuzzTookHandover(f *testing.F) {
	f.Add([]byte("drop-handover/1\nwas a\nnow b\nwhose me\nuntil 2026-01-01T00:00:00Z\n"), make([]byte, 64))
	f.Add([]byte("drop-plate/1\n"), make([]byte, 64))
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, signed, sig []byte) {
		over, err := Took(signed, sig, time.Unix(0, 0))
		if err != nil {
			return
		}

		if string(over.Bytes()) != string(signed) {
			t.Fatalf("a handover came back that is not what was checked:\n got %q\nwant %q", over.Bytes(), signed)
		}
		// The one thing that must never come back: a machine that became itself, or nothing.
		if over.Was == over.Now {
			t.Fatal("a handover came back saying a machine became itself")
		}
		if over.Was.IsZero() || over.Now.IsZero() {
			t.Fatal("a handover came back naming nothing")
		}
		if !plain.Fit(over.Whose, MaxName) {
			t.Fatalf("a handover came back naming %q, which cannot be shown", over.Whose)
		}
	})
}

// A signature that verifies has to be a signature somebody made. Random bytes against a real
// machine's id must never come back as one.
func FuzzASignatureIsNotGuessable(f *testing.F) {
	sk, err := key.GenerateSecretKey()
	if err != nil {
		f.Fatal(err)
	}
	id := sk.Public().EndpointID()

	real := Handover{Was: id, Now: sk.Public().EndpointID(), Whose: "me", Until: time.Unix(1, 0)}
	f.Add(real.Bytes(), make([]byte, 64))

	f.Fuzz(func(t *testing.T, signed, sig []byte) {
		if err := verified(id, signed, sig); err == nil && len(sig) == 64 {
			// Only a signature this key actually made may verify, and the fuzzer does not have
			// the secret half.
			if !sk.Public().EndpointID().Equal(id) {
				return
			}
			t.Fatalf("bytes nobody signed verified against %s", id)
		}
	})
}
