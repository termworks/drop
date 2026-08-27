package metal

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpm2/transport"
	"github.com/google/go-tpm/tpm2/transport/linuxtpm"

	"lukechampine.com/blake3"
)

// The TPM: the one place on a machine that can hold a secret the machine itself cannot read back.
//
// Two things are wanted from it. The first is a name: a key made from the chip's own seed, which is
// the same key every time on this machine and cannot be worked out on any other, so wiping the disk
// leaves the name alone and copying the disk does not take it along. The second is sealing, which
// is the same property pointed at somebody else's secret.
//
// Neither is available everywhere. Plenty of machines have no TPM, and plenty that do keep it for
// root. So everything here reports that it could not, and the caller carries on with a serial
// number — worse, and much better than refusing to run.

// chipAt is where a TPM is reached, best first. The resource manager multiplexes, so drop asking
// for a key does not lock the chip against everything else on the machine; the raw device is what
// is left on a kernel too old to have one.
var chipAt = []string{"/dev/tpmrm0", "/dev/tpm0"}

// opened is a TPM and the way to put it down.
func opened() (transport.TPMCloser, error) {
	var last error
	for _, at := range chipAt {
		if _, err := os.Stat(at); err != nil {
			last = err
			continue
		}
		chip, err := linuxtpm.Open(at)
		if err != nil {
			last = fmt.Errorf("opening %s: %w", at, err)
			continue
		}
		return chip, nil
	}
	if last == nil {
		last = fmt.Errorf("there is no TPM device on this machine")
	}
	return nil, last
}

// Sealing reports whether this machine has a TPM drop can actually reach, which is not the same as
// having one: the usual reason for a no is that the device is there and belongs to root.
func Sealing() bool {
	chip, err := opened()
	if err != nil {
		return false
	}
	chip.Close()
	return true
}

// rooted makes the storage key everything else here hangs off, and hands back the way to drop it.
//
// The template is the one the specification names, and it is deterministic: the same chip and the
// same template give the same key on every call and after every reinstall, because the seed it is
// derived from is inside the chip and outlives anything done to the disk. Clearing the TPM is what
// changes it, and clearing the TPM is meant to mean "this is somebody else's machine now".
func rooted(chip transport.TPM) (*tpm2.CreatePrimaryResponse, func(), error) {
	made, err := tpm2.CreatePrimary{
		PrimaryHandle: tpm2.TPMRHOwner,
		InPublic:      tpm2.New2B(tpm2.ECCSRKTemplate),
	}.Execute(chip)
	if err != nil {
		return nil, nil, fmt.Errorf("asking the TPM for this machine's storage key: %w", err)
	}

	drop := func() {
		tpm2.FlushContext{FlushHandle: made.ObjectHandle}.Execute(chip)
	}
	return made, drop, nil
}

// fromChip is the machine as its TPM knows it.
//
// What is derived from is the public half of the storage key. It is public, and it is still the
// right thing: it is unique to this chip and reproduced only by this chip, and the material never
// has to be the secret when the secret is what produced it.
func fromChip() (Mark, error) {
	chip, err := opened()
	if err != nil {
		return Mark{}, err
	}
	defer chip.Close()

	made, drop, err := rooted(chip)
	if err != nil {
		return Mark{}, err
	}
	defer drop()

	raw := tpm2.Marshal(made.OutPublic)
	if len(raw) == 0 {
		return Mark{}, fmt.Errorf("the TPM gave back a storage key with nothing in it")
	}
	sum := blake3.Sum256(raw)
	return Mark{From: Chip, Says: "the key this machine's TPM holds", raw: sum[:]}, nil
}

// Seal locks something up so that only this machine can open it again.
//
// What comes back is written down wherever the caller likes: it is useless anywhere else, and
// useless here if the TPM is cleared. That is the point — a backup carried to another machine
// carries this with it and it will not open.
func Seal(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, fmt.Errorf("sealing nothing")
	}

	chip, err := opened()
	if err != nil {
		return nil, err
	}
	defer chip.Close()

	made, drop, err := rooted(chip)
	if err != nil {
		return nil, err
	}
	defer drop()

	locked, err := tpm2.Create{
		ParentHandle: tpm2.AuthHandle{
			Handle: made.ObjectHandle,
			Name:   made.Name,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InSensitive: tpm2.TPM2BSensitiveCreate{
			Sensitive: &tpm2.TPMSSensitiveCreate{
				Data: tpm2.NewTPMUSensitiveCreate(&tpm2.TPM2BSensitiveData{Buffer: plain}),
			},
		},
		InPublic: tpm2.New2B(tpm2.TPMTPublic{
			Type:    tpm2.TPMAlgKeyedHash,
			NameAlg: tpm2.TPMAlgSHA256,
			ObjectAttributes: tpm2.TPMAObject{
				FixedTPM:     true,
				FixedParent:  true,
				UserWithAuth: true,
				NoDA:         true,
			},
		}),
	}.Execute(chip)
	if err != nil {
		return nil, fmt.Errorf("asking the TPM to seal this: %w", err)
	}

	return joined(tpm2.Marshal(locked.OutPublic), tpm2.Marshal(locked.OutPrivate)), nil
}

// Unseal opens what this machine sealed, and refuses what another machine did.
func Unseal(sealed []byte) ([]byte, error) {
	pub, priv, err := split(sealed)
	if err != nil {
		return nil, err
	}

	chip, err := opened()
	if err != nil {
		return nil, err
	}
	defer chip.Close()

	made, drop, err := rooted(chip)
	if err != nil {
		return nil, err
	}
	defer drop()

	public, err := tpm2.Unmarshal[tpm2.TPM2BPublic](pub)
	if err != nil {
		return nil, fmt.Errorf("reading back what was sealed: %w", err)
	}
	private, err := tpm2.Unmarshal[tpm2.TPM2BPrivate](priv)
	if err != nil {
		return nil, fmt.Errorf("reading back what was sealed: %w", err)
	}

	held, err := tpm2.Load{
		ParentHandle: tpm2.AuthHandle{
			Handle: made.ObjectHandle,
			Name:   made.Name,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InPublic:  *public,
		InPrivate: *private,
	}.Execute(chip)
	if err != nil {
		return nil, fmt.Errorf("this machine's TPM will not take what was sealed: %w", err)
	}
	defer tpm2.FlushContext{FlushHandle: held.ObjectHandle}.Execute(chip)

	open, err := tpm2.Unseal{
		ItemHandle: tpm2.NamedHandle{Handle: held.ObjectHandle, Name: held.Name},
	}.Execute(chip)
	if err != nil {
		return nil, fmt.Errorf("this machine's TPM will not open what was sealed: %w", err)
	}
	return open.OutData.Buffer, nil
}

// joined and split keep the two halves of a sealed thing in one run of bytes, length first, so
// reading them back does not depend on either half's own idea of where it ends.
func joined(pub, priv []byte) []byte {
	out := make([]byte, 4, 4+len(pub)+len(priv))
	binary.BigEndian.PutUint32(out, uint32(len(pub)))
	out = append(out, pub...)
	return append(out, priv...)
}

func split(raw []byte) (pub, priv []byte, err error) {
	if len(raw) < 4 {
		return nil, nil, fmt.Errorf("what was sealed is %d bytes, which is not enough to be anything", len(raw))
	}
	n := binary.BigEndian.Uint32(raw[:4])
	if int(n) > len(raw)-4 {
		return nil, nil, fmt.Errorf("what was sealed says its first half is %d bytes and there are %d", n, len(raw)-4)
	}
	return raw[4 : 4+n], raw[4+n:], nil
}
