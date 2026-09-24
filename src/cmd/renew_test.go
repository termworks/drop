package cmd

import (
	stdbytes "bytes"
	"os/exec"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/user"
)

// A key that signs through a command — a YubiKey, as ssh-keygen drives one — is not asked in the
// middle of a hello. The machine running low is written down, signed for when the key can, and its
// fresh badge handed over the next time it says hello.
func TestABadgeIsRenewedWhenTheKeyCanSign(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("needs ssh-keygen to stand in for the hardware")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := wearBadge(); err != nil {
		t.Fatal(err)
	}
	where, err := user.Where()
	if err != nil {
		t.Fatal(err)
	}
	user.SignWith("ssh-keygen -Y sign -f " + where + " -n " + user.Namespace)
	defer user.SignWith("")
	if _, quiet := user.Quiet(); quiet {
		t.Fatal("a key signed through a command was taken as one that signs without asking")
	}

	self, err := node.LocalID()
	if err != nil {
		t.Fatal(err)
	}
	r := &renewals{due: map[string]string{}, signed: map[string][]byte{}, tried: map[string]time.Time{}, nudge: make(chan struct{}, 1)}
	low := proto.Badged{Key: myKey(), As: "phone", Until: time.Now().Add(10 * 24 * time.Hour)}

	if got := r.ready(self, low); got != nil {
		t.Fatal("a badge was handed over before one was signed")
	}
	if n := len(r.waiting()); n != 1 {
		t.Fatalf("%d machines are waiting, want the one running low", n)
	}
	n, err := r.sign(time.Now(), true)
	if err != nil || n != 1 {
		t.Fatalf("signed %d (%v)", n, err)
	}
	bundle := r.ready(self, low)
	if bundle == nil {
		t.Fatal("the fresh badge is not handed over at the next hello")
	}
	at := stdbytes.Index(bundle, []byte("-----BEGIN SSH SIGNATURE-----"))
	if at < 0 {
		t.Fatalf("what is handed over is not a badge and a signature: %q", bundle)
	}
	fresh, err := user.Read(bundle[:at], bundle[at:], time.Now())
	if err != nil || fresh.Name != "phone" || fresh.Device != self.String() || user.Due(fresh.Until, time.Now()) {
		t.Fatalf("the fresh badge reads as %+v (%v)", fresh, err)
	}

	r.took(self)
	if len(r.waiting()) != 0 || r.ready(self, proto.Badged{Key: myKey(), Until: fresh.Until}) != nil && len(r.signed) != 0 {
		t.Fatal("a machine with its fresh badge is still waiting")
	}
}

// A key that could not sign is not asked again until later: a touch key blinks every time.
func TestABadgeThatCouldNotBeSignedWaits(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := wearBadge(); err != nil {
		t.Fatal(err)
	}
	user.SignWith("false")
	defer user.SignWith("")

	self, err := node.LocalID()
	if err != nil {
		t.Fatal(err)
	}
	r := &renewals{due: map[string]string{}, signed: map[string][]byte{}, tried: map[string]time.Time{}, nudge: make(chan struct{}, 1)}
	r.ready(self, proto.Badged{Key: myKey(), As: "phone", Until: time.Now().Add(time.Hour)})

	now := time.Now()
	if n, err := r.sign(now, false); n != 0 || err == nil {
		t.Fatalf("a key that cannot sign signed %d (%v)", n, err)
	}
	if n, err := r.sign(now.Add(time.Hour), false); n != 0 || err != nil {
		t.Fatalf("it was tried again within the hour: %d (%v)", n, err)
	}
}
