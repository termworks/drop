package user

import (
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// A badge small enough to ring a doorbell with.
//
// A machine that holds its person's key and knows none of their other machines says where it is
// under a record worked out from the public key, and has to prove there that the key is its
// person's. The badge it wears is that proof, and most of it is already known to whoever checks:
// the key is theirs, and the machine is the record's. What is left — when it runs out, the
// signature, and the name — fits in the few hundred bytes a record carries.

// MaxProof is the most a proof may take, leaving the record room for the machine it is about.
const MaxProof = 170

var ringText = base64.RawURLEncoding

// RingProof is a badge less what whoever checks it already knows. A key whose signatures are too
// large to fit, which is what an RSA key makes, cannot ring.
func RingProof(b Badge, sig []byte) (string, error) {
	block, _ := pem.Decode(sig)
	if block == nil || block.Type != pemType || len(block.Bytes) < len(magic) {
		return "", errors.New("that is not an ssh signature")
	}
	var held struct {
		Version   uint32
		PublicKey string
		Namespace string
		Reserved  string
		Hash      string
		Signature string
	}
	if err := ssh.Unmarshal(block.Bytes[len(magic):], &held); err != nil {
		return "", fmt.Errorf("reading the signature: %w", err)
	}
	var s ssh.Signature
	if err := ssh.Unmarshal([]byte(held.Signature), &s); err != nil {
		return "", fmt.Errorf("reading the signature itself: %w", err)
	}
	if s.Format != signedAs(b.User) || held.Hash != hashName || held.Namespace != Namespace {
		return "", fmt.Errorf("a %s key signs more than a doorbell carries", b.User.Type())
	}

	rest := "-"
	if len(s.Rest) > 0 {
		rest = ringText.EncodeToString(s.Rest)
	}
	out := strings.Join([]string{strconv.FormatInt(b.Until.Unix(), 10), ringText.EncodeToString(s.Blob), rest, b.Name}, " ")
	if len(out) > MaxProof {
		return "", errors.New("this machine's badge is too long to ring with: a shorter name fits")
	}
	return out, nil
}

// RungBadge is the badge a proof stands for, checked: signed by who, for device, and not run out.
func RungBadge(proof string, who ssh.PublicKey, device string, now time.Time) (Badge, error) {
	parts := strings.SplitN(proof, " ", 4)
	if len(parts) != 4 || signedAs(who) == "" {
		return Badge{}, errors.New("that is not a doorbell proof")
	}
	until, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Badge{}, errors.New("that proof says no time")
	}
	blob, err := ringText.DecodeString(parts[1])
	if err != nil {
		return Badge{}, errors.New("that proof's signature is unreadable")
	}
	var rest []byte
	if parts[2] != "-" {
		if rest, err = ringText.DecodeString(parts[2]); err != nil {
			return Badge{}, errors.New("that proof's signature is unreadable")
		}
	}

	badge := Badge{User: who, Device: device, Name: parts[3], Until: time.Unix(until, 0).UTC()}
	sig := &ssh.Signature{Format: signedAs(who), Blob: blob, Rest: rest}
	armoured := pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: armour(who, Namespace, sig)})
	return Read(badge.Bytes(), armoured, now)
}

// signedAs is the format a key's signatures carry, when it is the key's own type: every kind but
// RSA, whose signatures name the hash instead and are far too large to ring with anyway.
func signedAs(key ssh.PublicKey) string {
	if key == nil || key.Type() == ssh.KeyAlgoRSA {
		return ""
	}
	return key.Type()
}
