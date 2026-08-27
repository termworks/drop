package user

import (
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/plain"
)

// A badge is what turns "some machine" into "a machine of bob's", and it arrives from whoever
// dialled. Everything before the signature check runs for a stranger.
func FuzzReadBadge(f *testing.F) {
	f.Add([]byte("drop-badge/1\nuser ssh-ed25519 AAAA x\ndevice abc\nname laptop\nuntil 2026-01-01T00:00:00Z\n"), []byte{})
	f.Add([]byte("drop-badge/1\n"), []byte{})
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, signed, sig []byte) {
		badge, err := Read(signed, sig, time.Unix(0, 0))
		if err != nil {
			return
		}

		// Nothing gets past Read without being written exactly the way a badge is written, so what
		// was checked and what is used are the same bytes.
		if string(badge.Bytes()) != string(signed) {
			t.Fatalf("a badge came back that is not what was checked:\n got %q\nwant %q", badge.Bytes(), signed)
		}
		if badge.User == nil || badge.Device == "" {
			t.Fatal("a badge came back saying nothing about who owns what")
		}
		if badge.Expired(time.Unix(0, 0)) {
			t.Fatal("a badge came back that had already run out")
		}
		// The name is printed beside every machine in a listing.
		if badge.Name != "" && !plain.Fit(badge.Name, MaxName) {
			t.Fatalf("a badge came back named %q, which cannot be shown", badge.Name)
		}
	})
}

// The signature format underneath the badge, fed bytes nobody signed.
func FuzzVerify(f *testing.F) {
	f.Add([]byte("-----BEGIN SSH SIGNATURE-----\nrubbish\n-----END SSH SIGNATURE-----\n"), []byte("message"))
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, armoured, message []byte) {
		key, err := Verify(armoured, message, Namespace)
		if err == nil && key == nil {
			t.Fatal("a signature verified and named no key")
		}
	})
}
